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
	"edp-admin-console/context"
	"edp-admin-console/models/command"
	"edp-admin-console/models/query"
	"edp-admin-console/service"
	"edp-admin-console/service/platform"
	"edp-admin-console/util"
	"fmt"
	"github.com/astaxie/beego"
	"html/template"
	"path"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"strings"
)

var log = logf.Log.WithName("codebase-controller")

type ApplicationController struct {
	beego.Controller
	CodebaseService  service.CodebaseService
	EDPTenantService service.EDPTenantService
	BranchService    service.CodebaseBranchService
	GitServerService service.GitServerService
	SlaveService     service.SlaveService
	JobProvisioning  service.JobProvisioning

	IntegrationStrategies []string
	BuildTools            []string
	DeploymentScript      []string
}

const (
	paramWaitingForCodebase = "waitingforcodebase"

	OtherLanguage = "other"
)

func (c *ApplicationController) GetApplicationsOverviewPage() {
	flash := beego.ReadFromRequest(&c.Controller)
	if flash.Data["success"] != "" {
		c.Data["Success"] = true
	}

	applications, err := c.CodebaseService.GetCodebasesByCriteria(query.CodebaseCriteria{
		Type: query.App,
	})
	applications = addCodebaseInProgressIfAny(applications, c.GetString(paramWaitingForCodebase))
	if err != nil {
		c.Abort("500")
		return
	}

	contextRoles := c.GetSession("realm_roles").([]string)
	c.Data["EDPVersion"] = context.EDPVersion
	c.Data["Username"] = c.Ctx.Input.Session("username")
	c.Data["HasRights"] = isAdmin(contextRoles)
	c.Data["Codebases"] = applications
	c.Data["Type"] = query.App
	c.Data["xsrfdata"] = template.HTML(c.XSRFFormHTML())
	c.TplName = "codebase.html"
}

func addCodebaseInProgressIfAny(codebases []*query.Codebase, codebaseInProgress string) []*query.Codebase {
	if codebaseInProgress != "" {
		for _, codebase := range codebases {
			if codebase.Name == codebaseInProgress {
				return codebases
			}
		}

		log.Info("adding codebase which is going to be created to the list", "name", codebaseInProgress)
		app := query.Codebase{
			Name:   codebaseInProgress,
			Status: query.Inactive,
		}
		codebases = append(codebases, &app)
	}
	return codebases
}

func (c *ApplicationController) GetCreateApplicationPage() {
	flash := beego.ReadFromRequest(&c.Controller)
	isVcsEnabled, err := c.EDPTenantService.GetVcsIntegrationValue()

	if err != nil {
		c.Abort("500")
		return
	}

	if flash.Data["error"] != "" {
		c.Data["Error"] = flash.Data["error"]
	}

	contains := doesIntegrationStrategiesContainImportStrategy(c.IntegrationStrategies)
	if contains {
		log.Info("Import strategy is used.")

		gitServers, err := c.GitServerService.GetServers(query.GitServerCriteria{Available: true})
		if err != nil {
			c.Abort("500")
			return
		}
		log.Info("fetched Git Servers", "git servers", gitServers)

		c.Data["GitServers"] = gitServers
	}

	s, err := c.SlaveService.GetAllSlaves()
	if err != nil {
		c.Abort("500")
		return
	}

	p, err := c.JobProvisioning.GetAllJobProvisioners()
	if err != nil {
		c.Abort("500")
		return
	}

	c.Data["EDPVersion"] = context.EDPVersion
	c.Data["Username"] = c.Ctx.Input.Session("username")
	c.Data["IsVcsEnabled"] = isVcsEnabled
	c.Data["Type"] = query.App
	c.Data["CodeBaseIntegrationStrategy"] = true
	c.Data["IntegrationStrategies"] = c.IntegrationStrategies
	c.Data["JenkinsSlaves"] = s
	c.Data["BuildTools"] = c.BuildTools
	c.Data["JobProvisioners"] = p
	c.Data["DeploymentScripts"] = c.DeploymentScript
	c.Data["IsOpenshift"] = platform.IsOpenshift()
	c.Data["xsrfdata"] = template.HTML(c.XSRFFormHTML())
	c.TplName = "create_application.html"
}

func doesIntegrationStrategiesContainImportStrategy(integrationStrategies []string) bool {
	return contains(integrationStrategies, "import")
}

func contains(a []string, x string) bool {
	for _, n := range a {
		if x == strings.ToLower(n) {
			return true
		}
	}
	return false
}

func (c *ApplicationController) CreateApplication() {
	flash := beego.NewFlash()
	codebase := c.extractApplicationRequestData()
	errMsg := ValidRequestData(codebase)
	if errMsg != nil {
		log.Info("failed to validate request data", "message", errMsg.Message)
		flash.Error(errMsg.Message)
		flash.Store(&c.Controller)
		c.Redirect("/admin/edp/application/create", 302)
		return
	}
	LogRequestData(codebase)

	createdObject, err := c.CodebaseService.CreateCodebase(codebase)

	if err != nil {
		if err.Error() == "CODEBASE_ALREADY_EXISTS" {
			flash.Error("Application %s name is already exists.", codebase.Name)
			flash.Store(&c.Controller)
			c.Redirect("/admin/edp/application/create", 302)
			return
		}
		c.Abort("500")
		return
	}

	log.Info("application object is saved into cluster", "name", createdObject.Name)
	flash.Success("Application object is created.")
	flash.Store(&c.Controller)
	c.Redirect(fmt.Sprintf("/admin/edp/application/overview?%s=%s#codebaseSuccessModal", paramWaitingForCodebase, codebase.Name), 302)
}

func (c *ApplicationController) extractApplicationRequestData() command.CreateCodebase {
	codebase := command.CreateCodebase{
		Lang:             c.GetString("appLang"),
		BuildTool:        c.GetString("buildTool"),
		Strategy:         strings.ToLower(c.GetString("strategy")),
		Type:             "application",
		JenkinsSlave:     c.GetString("jenkinsSlave"),
		JobProvisioning:  c.GetString("jobProvisioning"),
		DeploymentScript: c.GetString("deploymentScript"),
	}

	if codebase.Strategy == "import" {
		codebase.GitServer = c.GetString("gitServer")
		gitRepoPath := c.GetString("gitRelativePath")
		codebase.GitUrlPath = &gitRepoPath
		codebase.Name = path.Base(*codebase.GitUrlPath)
	} else {
		codebase.Name = c.GetString("nameOfApp")
		codebase.GitServer = "gerrit"
	}

	if o := OtherLanguage; codebase.Lang == OtherLanguage {
		codebase.Framework = &o
	} else {
		framework := c.GetString("framework")
		codebase.Framework = &framework
	}

	isMultiModule, _ := c.GetBool("isMultiModule", false)
	codebase.MultiModule = isMultiModule

	if isMultiModule {
		multimoduleApp := fmt.Sprintf("%s-multimodule", *codebase.Framework)
		codebase.Framework = &multimoduleApp
	}

	repoUrl := c.GetString("gitRepoUrl")
	if repoUrl != "" {
		codebase.Repository = &command.Repository{
			Url: repoUrl,
		}

		isRepoPrivate, _ := c.GetBool("isRepoPrivate", false)
		if isRepoPrivate {
			codebase.Repository.Login = c.GetString("repoLogin")
			codebase.Repository.Password = c.GetString("repoPassword")
		}
	}

	vcsLogin := c.GetString("vcsLogin")
	vcsPassword := c.GetString("vcsPassword")
	if vcsLogin != "" && vcsPassword != "" {
		codebase.Vcs = &command.Vcs{
			Login:    vcsLogin,
			Password: vcsPassword,
		}
	}

	needRoute, _ := c.GetBool("needRoute", false)
	if needRoute {
		codebase.Route = &command.Route{
			Site: c.GetString("routeSite"),
		}
		if len(c.GetString("routePath")) > 0 {
			codebase.Route.Path = c.GetString("routePath")
		}
	}

	needDb, _ := c.GetBool("needDb", false)
	if needDb {
		codebase.Database = &command.Database{
			Kind:     c.GetString("database"),
			Version:  c.GetString("dbVersion"),
			Capacity: c.GetString("dbCapacity") + c.GetString("capacityExt"),
			Storage:  c.GetString("dbPersistentStorage"),
		}
	}
	codebase.Username = c.Ctx.Input.Session("username").(string)
	return codebase
}

func isAdmin(contextRoles []string) bool {
	if contextRoles == nil {
		return false
	}
	return util.Contains(contextRoles, beego.AppConfig.String("adminRole"))
}
