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

package controllers

import (
	"edp-admin-console/models"
	"edp-admin-console/models/command"
	"edp-admin-console/service/cd_pipeline"
	dberror "edp-admin-console/util/error/db-errors"
	"encoding/json"
	"fmt"
	"github.com/astaxie/beego"
	"github.com/astaxie/beego/validation"
	uuid "github.com/satori/go.uuid"
	"net/http"
)

type CDPipelineRestController struct {
	beego.Controller
	CDPipelineService cd_pipeline.CDPipelineService
}

func (c *CDPipelineRestController) Prepare() {
	c.EnableXSRF = false
}

func (c *CDPipelineRestController) GetCDPipelineByName() {
	pipelineName := c.GetString(":name")
	cdPipeline, err := c.CDPipelineService.GetCDPipelineByName(pipelineName)
	if err != nil {
		http.Error(c.Ctx.ResponseWriter, err.Error(), http.StatusInternalServerError)
		return
	}

	if cdPipeline == nil {
		nonAppMsg := fmt.Sprintf("Please check CD Pipeline name. It seems there's not %s pipeline.", pipelineName)
		http.Error(c.Ctx.ResponseWriter, nonAppMsg, http.StatusNotFound)
		return
	}

	c.Data["json"] = cdPipeline
	c.ServeJSON()
}

func (c *CDPipelineRestController) GetStage() {
	pipelineName := c.GetString(":pipelineName")
	stageName := c.GetString(":stageName")

	stage, err := c.CDPipelineService.GetStage(pipelineName, stageName)
	if err != nil {
		http.Error(c.Ctx.ResponseWriter, err.Error(), http.StatusInternalServerError)
		return
	}

	if stage == nil {
		http.Error(c.Ctx.ResponseWriter, "Please check request data.", http.StatusNotFound)
		return
	}

	c.Data["json"] = stage
	c.ServeJSON()
}

func (c *CDPipelineRestController) CreateCDPipeline() {
	var cdPipelineCreateCommand command.CDPipelineCommand
	err := json.NewDecoder(c.Ctx.Request.Body).Decode(&cdPipelineCreateCommand)
	if err != nil {
		http.Error(c.Ctx.ResponseWriter, err.Error(), http.StatusInternalServerError)
		return
	}
	errMsg := validateCDPipelineRequestData(cdPipelineCreateCommand)
	if errMsg != nil {
		log.Info("Failed to validate request data", "err", errMsg.Message)
		http.Error(c.Ctx.ResponseWriter, errMsg.Message, http.StatusBadRequest)
		return
	}
	log.Info("Request data is receieved to create CD pipeline",
		"pipeline", cdPipelineCreateCommand.Name, "applications", cdPipelineCreateCommand.Applications,
		"stages", cdPipelineCreateCommand.Stages, "services", cdPipelineCreateCommand.ThirdPartyServices)

	_, pipelineErr := c.CDPipelineService.CreatePipeline(cdPipelineCreateCommand)
	if pipelineErr != nil {

		switch pipelineErr.(type) {
		case *models.CDPipelineExistsError:
			http.Error(c.Ctx.ResponseWriter, fmt.Sprintf("cd pipeline %v is already exists", cdPipelineCreateCommand.Name), http.StatusFound)
			return
		case *models.NonValidRelatedBranchError:
			http.Error(c.Ctx.ResponseWriter, fmt.Sprintf("one or more applications have non valid branches: %v", cdPipelineCreateCommand.Applications), http.StatusBadRequest)
			return
		default:
			http.Error(c.Ctx.ResponseWriter, err.Error(), http.StatusInternalServerError)
			return
		}

	}

	c.Ctx.ResponseWriter.WriteHeader(http.StatusCreated)
}

func (c *CDPipelineRestController) UpdateCDPipeline() {
	var pipelineUpdateCommand command.CDPipelineCommand
	err := json.NewDecoder(c.Ctx.Request.Body).Decode(&pipelineUpdateCommand)
	if err != nil {
		http.Error(c.Ctx.ResponseWriter, err.Error(), http.StatusInternalServerError)
		return
	}

	pipelineUpdateCommand.Name = c.GetString(":name")

	errMsg := validateCDPipelineUpdateRequestData(pipelineUpdateCommand)
	if errMsg != nil {
		log.Info("Request data is not valid", "err", errMsg.Message)
		http.Error(c.Ctx.ResponseWriter, errMsg.Message, http.StatusBadRequest)
		return
	}
	log.Info("Request data is received to update CD pipeline",
		"pipeline", pipelineUpdateCommand.Name, "applications", pipelineUpdateCommand.Applications)

	err = c.CDPipelineService.UpdatePipeline(pipelineUpdateCommand)
	if err != nil {

		switch err.(type) {
		case *models.CDPipelineDoesNotExistError:
			http.Error(c.Ctx.ResponseWriter, fmt.Sprintf("cd pipeline %v doesn't exist", pipelineUpdateCommand.Name), http.StatusNotFound)
			return
		case *models.NonValidRelatedBranchError:
			http.Error(c.Ctx.ResponseWriter, fmt.Sprintf("one or more applications have non valid branches: %v", pipelineUpdateCommand.Name), http.StatusNotFound)
			return
		default:
			http.Error(c.Ctx.ResponseWriter, err.Error(), http.StatusInternalServerError)
			return
		}

	}

	c.Ctx.ResponseWriter.WriteHeader(http.StatusNoContent)
}

func validateCDPipelineRequestData(cdPipeline command.CDPipelineCommand) *ErrMsg {
	var isCDPipelineValid, isApplicationsValid, isStagesValid, isQualityGatesValid bool
	errMsg := &ErrMsg{"An internal error has occurred on server while validating CD Pipeline's request body.", http.StatusInternalServerError}
	valid := validation.Validation{}
	isCDPipelineValid, err := valid.Valid(cdPipeline)

	if err != nil {
		return errMsg
	}

	if cdPipeline.Applications != nil {
		for _, app := range cdPipeline.Applications {
			isApplicationsValid, err = valid.Valid(app)
			if err != nil {
				return errMsg
			}
		}
	}

	if cdPipeline.Stages != nil {
		for _, stage := range cdPipeline.Stages {

			isValid, err := validateQualityGates(valid, stage.QualityGates)
			if err != nil {
				return errMsg
			}
			isQualityGatesValid = isValid

			isStagesValid, err = valid.Valid(stage)
			if err != nil {
				return errMsg
			}
		}
	} else {
		err := &validation.Error{Key: "stages", Message: "can not be null"}
		valid.Errors = append(valid.Errors, err)
		isStagesValid = false
	}

	if isCDPipelineValid && isApplicationsValid && isStagesValid && isQualityGatesValid {
		return nil
	}

	return &ErrMsg{string(createErrorResponseBody(valid)), http.StatusBadRequest}
}

func (c *CDPipelineRestController) DeleteCDStage() {
	var sc command.DeleteStageCommand
	if err := json.NewDecoder(c.Ctx.Request.Body).Decode(&sc); err != nil {
		http.Error(c.Ctx.ResponseWriter, err.Error(), http.StatusInternalServerError)
		return
	}
	log.V(2).Info("request to delete cd stage has been retrieved",
		"pipeline", sc.CDPipelineName, "stage", sc.Name)
	if err := c.CDPipelineService.DeleteCDStage(sc.CDPipelineName, sc.Name); err != nil {
		if dberror.StageErrorOccurred(err) {
			serr := err.(dberror.RemoveStageRestriction)
			log.Error(err, serr.Message)
			http.Error(c.Ctx.ResponseWriter, serr.Message, http.StatusConflict)
			return
		}
		log.Error(err, "delete process is failed")
		http.Error(c.Ctx.ResponseWriter, "delete process is failed", http.StatusInternalServerError)
		return
	}
	log.V(2).Info("delete cd stage method is finished",
		"pipeline", sc.CDPipelineName, "stage", sc.Name)
	location := fmt.Sprintf("%s/%s", c.Ctx.Input.URL(), uuid.NewV4().String())
	c.Ctx.ResponseWriter.WriteHeader(200)
	c.Ctx.Output.Header("Location", location)
}
