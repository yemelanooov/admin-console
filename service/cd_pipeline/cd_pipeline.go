/*
 * Copyright 2019 EPAM Systems.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package cd_pipeline

import (
	"edp-admin-console/context"
	"edp-admin-console/k8s"
	"edp-admin-console/models"
	"edp-admin-console/models/command"
	"edp-admin-console/models/query"
	"edp-admin-console/repository"
	"edp-admin-console/service"
	ec "edp-admin-console/service/edp-component"
	"edp-admin-console/service/platform"
	"edp-admin-console/util/consts"
	dberror "edp-admin-console/util/error/db-errors"
	"fmt"
	"github.com/astaxie/beego/orm"
	edppipelinesv1alpha1 "github.com/epmd-edp/cd-pipeline-operator/v2/pkg/apis/edp/v1alpha1"
	"github.com/pkg/errors"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sort"
	"strings"
	"time"
)

type CDPipelineService struct {
	Clients               k8s.ClientSet
	ICDPipelineRepository repository.ICDPipelineRepository
	CodebaseService       service.CodebaseService
	BranchService         service.CodebaseBranchService
	EDPComponent          ec.EDPComponentService
}

type ErrMsg struct {
	Message    string
	StatusCode int
}

var log = logf.Log.WithName("cd-pipeline-service")

func (s *CDPipelineService) CreatePipeline(cdPipeline command.CDPipelineCommand) (*edppipelinesv1alpha1.CDPipeline, error) {
	log.V(2).Info("start creating CD Pipeline", "name", cdPipeline)
	exist, err := s.CodebaseService.CheckBranch(cdPipeline.Applications)
	if err != nil {
		return nil, err
	}

	if !exist {
		return nil, models.NewNonValidRelatedBranchError()
	}

	cdPipelineReadModel, err := s.GetCDPipelineByName(cdPipeline.Name)
	if err != nil {
		return nil, err
	}

	if cdPipelineReadModel != nil {
		log.V(2).Info("CD Pipeline already exists in DB.", "name", cdPipeline.Name)
		return nil, models.NewCDPipelineExistsError()
	}

	edpRestClient := s.Clients.EDPRestClient
	pipelineCR, err := s.getCDPipelineCR(cdPipeline.Name)
	if err != nil {
		return nil, err
	}

	if pipelineCR != nil {
		log.V(2).Info("CD Pipeline already exists in cluster.", "name", cdPipeline.Name)
		return nil, models.NewCDPipelineExistsError()
	}

	crd := &edppipelinesv1alpha1.CDPipeline{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v2.edp.epam.com/v1alpha1",
			Kind:       "CDPipeline",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      cdPipeline.Name,
			Namespace: context.Namespace,
		},
		Spec: convertPipelineData(cdPipeline),
		Status: edppipelinesv1alpha1.CDPipelineStatus{
			Available:       false,
			LastTimeUpdated: time.Now(),
			Status:          "initialized",
			Username:        cdPipeline.Username,
			Action:          "cd_pipeline_registration",
			Result:          "success",
			Value:           "inactive",
		},
	}

	cdPipelineCr := &edppipelinesv1alpha1.CDPipeline{}
	err = edpRestClient.Post().
		Namespace(context.Namespace).
		Resource("cdpipelines").
		Body(crd).
		Do().Into(cdPipelineCr)
	if err != nil {
		return nil, errors.Wrap(err, "an error has occurred while creating CD Pipeline object in cluster")
	}
	log.Info("CD Pipeline has been saved to cluster", "name", cdPipeline)

	if _, err = s.CreateStages(edpRestClient, cdPipeline); err != nil {
		return nil, errors.Wrap(err, "an error has occurred while creating Stages in cluster")
	}
	log.Info("Stages for CD Pipeline have been created in cluster", "pipe", cdPipeline.Name, "stages", cdPipeline.Stages)
	return cdPipelineCr, nil
}

func (s *CDPipelineService) GetCDPipelineByName(pipelineName string) (*query.CDPipeline, error) {
	log.V(2).Info("start execution of GetCDPipelineByName method...")
	cdPipeline, err := s.ICDPipelineRepository.GetCDPipelineByName(pipelineName)
	if err != nil {
		return nil, errors.Wrapf(err, "an error has occurred while getting CD Pipeline %v from db", pipelineName)
	}

	if cdPipeline != nil {
		if len(cdPipeline.Stage) != 0 {
			sortStagesByOrder(cdPipeline.Stage)
			createPlatformNames(cdPipeline.Stage, cdPipeline.Name)
			log.V(2).Info("stages were fetched", "count", len(cdPipeline.Stage), "values", cdPipeline.Stage)
		}
		for i, branch := range cdPipeline.CodebaseBranch {
			branch.AppName = branch.Codebase.Name
			cdPipeline.CodebaseBranch[i] = branch
		}

		matrix, err := fillCodebaseStageMatrix(&s.Clients, cdPipeline)
		if err == nil {
			cdPipeline.CodebaseStageMatrix = matrix
		}

		applicationsToPromote, err := s.CodebaseService.GetApplicationsToPromote(cdPipeline.Id)
		if err != nil {
			return nil, errors.Wrapf(err, "an error has occurred while getting Applications To Promote for CD Pipeline",
				"pipe id", cdPipeline.Id)
		}
		cdPipeline.ApplicationsToPromote = applicationsToPromote
		log.V(2).Info("CD Pipeline has been fetched from DB", "pipe", cdPipeline.Name)
	}
	return cdPipeline, nil
}

func (s *CDPipelineService) CreateStages(edpRestClient *rest.RESTClient, cdPipeline command.CDPipelineCommand) ([]edppipelinesv1alpha1.Stage, error) {
	log.V(2).Info("start creating stages", "stages", cdPipeline.Stages)
	if err := checkStagesInK8s(edpRestClient, cdPipeline.Name, cdPipeline.Stages); err != nil {
		return nil, errors.Wrap(err, "couldn't check stages in cluster")
	}

	stagesCr, err := saveStagesIntoK8s(edpRestClient, cdPipeline.Name, cdPipeline.Stages, cdPipeline.Username)
	if err != nil {
		return nil, err
	}
	return stagesCr, nil
}

func (s *CDPipelineService) GetAllPipelines(criteria query.CDPipelineCriteria) ([]*query.CDPipeline, error) {
	log.V(2).Info("start fetching all CD Pipelines...")
	cdPipelines, err := s.ICDPipelineRepository.GetCDPipelines(criteria)
	if err != nil {
		return nil, errors.Wrap(err, "an error has occurred while getting CD Pipelines from database")
	}
	log.V(2).Info("CD Pipelines were fetched", "count", len(cdPipelines), "values", cdPipelines)
	return cdPipelines, nil
}

func (s *CDPipelineService) UpdatePipeline(pipeline command.CDPipelineCommand) error {
	log.V(2).Info("start updating CD Pipeline", "name", pipeline.Name)
	if pipeline.Applications != nil {
		exist, err := s.CodebaseService.CheckBranch(pipeline.Applications)
		if err != nil {
			return err
		}
		if !exist {
			return models.NewNonValidRelatedBranchError()
		}
	}

	cdPipelineReadModel, err := s.GetCDPipelineByName(pipeline.Name)
	if err != nil {
		return err
	}
	if cdPipelineReadModel == nil {
		log.Info("CD Pipeline doesn't exist in DB.", "name", pipeline.Name)
		return models.NewCDPipelineDoesNotExistError()
	}

	pipelineCR, err := s.getCDPipelineCR(pipeline.Name)
	if err != nil {
		return err
	}
	if pipelineCR == nil {
		log.Info("CD Pipeline doesn't exist in cluster.", "name", pipeline.Name)
		return models.NewCDPipelineDoesNotExistError()
	}

	if pipeline.Applications != nil {
		log.V(2).Info("start updating Autotest",
			"pipe name", pipelineCR.Spec.Name, "apps", pipeline.Applications)
		var dockerStreams []string
		for _, v := range pipeline.Applications {
			dockerStreams = append(dockerStreams, v.InputDockerStream)
		}
		pipelineCR.Spec.InputDockerStreams = dockerStreams
	}

	pipelineCR.Spec.ApplicationsToPromote = pipeline.ApplicationToApprove
	pipelineCR.Status.LastTimeUpdated = time.Now()

	edpRestClient := s.Clients.EDPRestClient

	err = edpRestClient.Put().
		Namespace(context.Namespace).
		Resource("cdpipelines").
		Name(pipelineCR.Spec.Name).
		Body(pipelineCR).
		Do().
		Into(pipelineCR)

	if err != nil {
		return errors.Wrap(err, "an error has occurred while updating CD Pipeline cluster")
	}
	log.Info("CD Pipeline has been updated", "name", pipeline.Name)
	return nil
}

func sortStagesByOrder(stages []*query.Stage) {
	sort.Slice(stages, func(i, j int) bool {
		return stages[i].Order < stages[j].Order
	})
}

func (s *CDPipelineService) GetStage(cdPipelineName, stageName string) (*models.StageView, error) {
	log.V(2).Info("start fetching Stage", "name", stageName)
	stage, err := s.ICDPipelineRepository.GetStage(cdPipelineName, stageName)
	if err != nil {
		return nil, errors.Wrap(err, "an error has occurred while getting Stage from DB")
	}

	if stage == nil {
		log.V(2).Info("couldn't find Stage ", "stage", stageName, "pipe", cdPipelineName)
		return nil, nil
	}

	gates, err := s.ICDPipelineRepository.GetQualityGates(stage.Id)
	if err != nil {
		return nil, errors.Wrap(err, "an error has occurred while fetching Quality Gates from DB")
	}
	stage.QualityGates = gates
	log.V(2).Info("Stages have been fetched", "stage", stage, "gates", stage.QualityGates)
	return stage, nil
}

func createPlatformNames(stages []*query.Stage, cdPipelineName string) {
	for i, v := range stages {
		stages[i].PlatformProjectName = fmt.Sprintf("%s-%s-%s", context.Tenant, cdPipelineName, v.Name)
	}
}

func fillCodebaseStageMatrix(ocClient *k8s.ClientSet, cdPipeline *query.CDPipeline) (map[query.CDCodebaseStageMatrixKey]query.CDCodebaseStageMatrixValue, error) {
	if !platform.IsOpenshift() {
		return fillCodebaseStageMatrixK8s(ocClient, cdPipeline)
	}
	var matrix = make(map[query.CDCodebaseStageMatrixKey]query.CDCodebaseStageMatrixValue, len(cdPipeline.CodebaseBranch)*len(cdPipeline.Stage))

	for _, stage := range cdPipeline.Stage {

		dcs, err := ocClient.AppsV1Client.DeploymentConfigs(stage.PlatformProjectName).List(metav1.ListOptions{})
		if err != nil {
			return nil, errors.Wrap(err, "an error has occurred while getting project from cluster")
		}

		for _, codebase := range cdPipeline.CodebaseBranch {
			var key = query.CDCodebaseStageMatrixKey{
				CodebaseBranch: codebase,
				Stage:          stage,
			}
			var value = query.CDCodebaseStageMatrixValue{
				DockerVersion: "no deploy",
			}
			for _, dc := range dcs.Items {
				for _, container := range dc.Spec.Template.Spec.Containers {
					if container.Name == codebase.AppName {
						var containerImage = container.Image
						var delimeter = strings.LastIndex(containerImage, ":")
						if delimeter > 0 {
							value.DockerVersion = string(containerImage[(delimeter + 1):len(containerImage)])
						}
					}
				}
			}
			matrix[key] = value
		}

	}
	return matrix, nil
}

func fillCodebaseStageMatrixK8s(ocClient *k8s.ClientSet, cdPipeline *query.CDPipeline) (map[query.CDCodebaseStageMatrixKey]query.CDCodebaseStageMatrixValue, error) {
	var matrix = make(map[query.CDCodebaseStageMatrixKey]query.CDCodebaseStageMatrixValue, len(cdPipeline.CodebaseBranch)*len(cdPipeline.Stage))
	for _, stage := range cdPipeline.Stage {

		dcs, err := ocClient.ExtensionClient.Deployments(stage.PlatformProjectName).List(metav1.ListOptions{})
		if err != nil {
			return nil, errors.Wrap(err, "an error has occurred while getting project from cluster")
		}

		for _, codebase := range cdPipeline.CodebaseBranch {
			var key = query.CDCodebaseStageMatrixKey{
				CodebaseBranch: codebase,
				Stage:          stage,
			}
			var value = query.CDCodebaseStageMatrixValue{
				DockerVersion: "no deploy",
			}
			for _, dc := range dcs.Items {
				for _, container := range dc.Spec.Template.Spec.Containers {
					if container.Name == codebase.AppName {
						var containerImage = container.Image
						var delimeter = strings.LastIndex(containerImage, ":")
						if delimeter > 0 {
							value.DockerVersion = string(containerImage[(delimeter + 1):len(containerImage)])
						}
					}
				}
			}
			matrix[key] = value
		}

	}
	return matrix, nil
}

func convertPipelineData(cdPipeline command.CDPipelineCommand) edppipelinesv1alpha1.CDPipelineSpec {
	var dockerStreams []string
	for _, app := range cdPipeline.Applications {
		dockerStreams = append(dockerStreams, app.InputDockerStream)
	}
	return edppipelinesv1alpha1.CDPipelineSpec{
		Name:                  cdPipeline.Name,
		InputDockerStreams:    dockerStreams,
		ThirdPartyServices:    cdPipeline.ThirdPartyServices,
		ApplicationsToPromote: cdPipeline.ApplicationToApprove,
	}
}

func (s *CDPipelineService) getCDPipelineCR(pipelineName string) (*edppipelinesv1alpha1.CDPipeline, error) {
	edpRestClient := s.Clients.EDPRestClient
	cdPipeline := &edppipelinesv1alpha1.CDPipeline{}

	err := edpRestClient.Get().Namespace(context.Namespace).Resource("cdpipelines").Name(pipelineName).Do().Into(cdPipeline)
	if k8serrors.IsNotFound(err) {
		log.Info("pipeline doesn't exist in cluster.", "name", pipelineName)
		return nil, nil
	}
	if err != nil {
		return nil, errors.Wrap(err, "an error has occurred while getting cd pipeline from cluster")
	}
	return cdPipeline, nil
}

func createCrd(cdPipelineName string, stage command.CDStageCommand) edppipelinesv1alpha1.Stage {
	return edppipelinesv1alpha1.Stage{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v2.edp.epam.com/v1alpha1",
			Kind:       "Stage",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s", cdPipelineName, stage.Name),
			Namespace: context.Namespace,
		},
		Spec: edppipelinesv1alpha1.StageSpec{
			Name:         stage.Name,
			Description:  stage.Description,
			TriggerType:  stage.TriggerType,
			Order:        stage.Order,
			CdPipeline:   cdPipelineName,
			QualityGates: stage.QualityGates,
			Source:       stage.Source,
		},
		Status: edppipelinesv1alpha1.StageStatus{
			Available:       false,
			LastTimeUpdated: time.Now(),
			Status:          "initialized",
			Username:        stage.Username,
			Action:          "cd_stage_registration",
			Result:          "success",
			Value:           "inactive",
		},
	}
}

func saveStagesIntoK8s(edpRestClient *rest.RESTClient, cdPipelineName string, stages []command.CDStageCommand, username string) ([]edppipelinesv1alpha1.Stage, error) {
	var stagesCr []edppipelinesv1alpha1.Stage
	for _, stage := range stages {
		stage.Username = username
		crd := createCrd(cdPipelineName, stage)
		stageCr := edppipelinesv1alpha1.Stage{}
		err := edpRestClient.Post().
			Namespace(context.Namespace).
			Resource("stages").
			Body(&crd).
			Do().Into(&stageCr)
		if err != nil {
			return nil, errors.Wrap(err, "an error has occurred while creating Stage in cluster")
		}
		log.Info("stage has been saved into cluster", "name", stage.Name)
		stagesCr = append(stagesCr, stageCr)
	}
	return stagesCr, nil
}

func checkStagesInK8s(edpRestClient *rest.RESTClient, cdPipelineName string, stages []command.CDStageCommand) error {
	for _, stage := range stages {
		stagesCr := &edppipelinesv1alpha1.Stage{}
		stageName := fmt.Sprintf("%s-%s", cdPipelineName, stage.Name)
		err := edpRestClient.Get().Namespace(context.Namespace).Resource("stages").Name(stageName).Do().Into(stagesCr)

		if k8serrors.IsNotFound(err) {
			log.V(2).Info("stage doesn't exist", "name", stage.Name)
			continue
		}

		if err != nil {
			return errors.Wrap(err, "an error has occurred while getting Stage from cluster")
		}

		if stagesCr != nil {
			return fmt.Errorf("stage %v already exists", stage.Name)
		}
	}
	return nil
}

func (s CDPipelineService) DeleteCDStage(pipelineName, stageName string) error {
	log.V(2).Info("start deleting cd stage", "stage", stageName, "pipe", pipelineName)
	if err := s.canStageBeDeleted(pipelineName, stageName); err != nil {
		return err
	}

	sn := fmt.Sprintf("%v-%v", pipelineName, stageName)
	if err := s.deleteStage(sn); err != nil {
		return err
	}
	log.Info("stage has been marked for deletion", "name", sn)
	return nil
}

func (s CDPipelineService) canStageBeDeleted(pipelineName, stageName string) error {
	mso, err := s.ICDPipelineRepository.SelectMaxOrderBetweenStages(pipelineName)
	if err != nil {
		return err
	}
	so, err := s.ICDPipelineRepository.SelectStageOrder(pipelineName, stageName)
	if err != nil {
		if err == orm.ErrNoRows {
			return dberror.RemoveStageRestriction{
				Status:  dberror.StatusRemoveStageRestriction,
				Message: fmt.Sprintf("%v CD Stage wasn't found in CD Pipeline %v", stageName, pipelineName),
			}
		}
		return err
	}
	u, err := s.ICDPipelineRepository.DoesStageUsedAsSourceInAnother(pipelineName, stageName)
	if err != nil {
		return err
	}

	if *mso == 0 && *so == 0 {
		return dberror.RemoveStageRestriction{
			Status:  dberror.StatusRemoveStageRestriction,
			Message: fmt.Sprintf("Couldn't delete because of CD Pipeline %v contains only one stage %v", pipelineName, stageName),
		}
	}
	if *mso != *so {
		return dberror.RemoveStageRestriction{
			Status:  dberror.StatusRemoveStageRestriction,
			Message: fmt.Sprintf("%v CD Stage isn't the last in %v CD Pipeline", stageName, pipelineName),
		}
	}
	if u {
		return dberror.RemoveStageRestriction{
			Status:  dberror.StatusRemoveStageRestriction,
			Message: fmt.Sprintf("%v CD Stage is used as a source in another CD Pipeline", stageName),
		}
	}
	return nil
}

func (s CDPipelineService) deleteStage(name string) error {
	log.V(2).Info("start executing stage delete request", "stage", name)
	i := &edppipelinesv1alpha1.Stage{}
	err := s.Clients.EDPRestClient.Delete().
		Namespace(context.Namespace).
		Resource(consts.StagePlural).
		Name(name).
		Do().Into(i)
	if err != nil {
		return errors.Wrapf(err, "couldn't delete stage %v from cluster", name)
	}
	log.V(2).Info("end executing stage delete request", "stage", name)
	return nil
}
