package service

import (
	"edp-admin-console/k8s"
	"edp-admin-console/models"
	"edp-admin-console/repository"
	"edp-admin-console/util"
	"fmt"
	"github.com/astaxie/beego"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"log"
	"strconv"
	"strings"
)

type EDPTenantService struct {
	EDPTenantRep repository.EDPTenantRep
	Clients      k8s.ClientSet
}

var (
	edpComponentNames = []string{"Jenkins", "Gerrit", "Sonar", "Nexus", "Cockpit"}
	wildcard          = beego.AppConfig.String("wildcard")
)

func (edpService EDPTenantService) GetEDPTenants(resourceAccess map[string][]string) ([]*models.EDPTenant, error) {
	edpTenantNames := getEdpTenantNamesWithoutSuffix(resourceAccess)
	if edpTenantNames == nil {
		log.Println("There aren't edp tenants to display.")
		return nil, nil
	}

	edpSpecs, err := edpService.EDPTenantRep.GetAllEDPTenantsByNames(edpTenantNames)
	if err != nil {
		log.Printf("Couldn't get all EDP specifications. Reason: %v\n", err)
		return nil, err
	}

	return edpSpecs, nil
}

func (edpService EDPTenantService) GetEDPVersionByName(edpTenantName string) (string, error) {
	version, err := edpService.EDPTenantRep.GetEdpVersionByName(edpTenantName)
	if err != nil {
		log.Printf("An error has occurred while getting version of %s EDP.", edpTenantName)
		return "", err
	}
	return version, nil
}

func (edpService EDPTenantService) GetEDPComponents(edpTenantName string) map[string]string {
	var compWithLinks = make(map[string]string, len(edpComponentNames))
	for _, val := range edpComponentNames {
		link := fmt.Sprintf("https://%s-%s-edp-cicd.%s", strings.ToLower(val), edpTenantName, wildcard)
		compWithLinks[val] = link
	}
	return compWithLinks
}

func (this EDPTenantService) GetVcsIntegrationValue() (bool, error) {
	clientset := this.Clients.CoreClient

	res, err := clientset.ConfigMaps(beego.AppConfig.String("namespace")).Get("user-settings", metav1.GetOptions{})

	if err != nil {
		log.Printf("An error has occurred while getting user settings: %s", err)
		return false, err
	}

	var vcsEnabled = res.Data["vcs_integration_enabled"]

	if len(vcsEnabled) == 0 {
		log.Println("vcs_integration_enabled property doesn't exist. Configured default value: 'vcs_integration_enabled=false'")
		return false, nil
	}

	result, err := strconv.ParseBool(vcsEnabled)

	if err != nil {
		log.Printf("An error has occurred while parsing 'vcs_integration_enabled=false': %s", err)
		return false, err
	}

	return result, nil
}

func getEdpTenantNamesWithoutSuffix(resourceAccess map[string][]string) []string {
	var edpTenants []string
	suffix := "-edp"
	for key, value := range resourceAccess {
		if strings.HasSuffix(key, suffix) && util.Contains(value, beego.AppConfig.String("adminRole")) {
			edpTenants = append(edpTenants, strings.TrimSuffix(key, suffix))
		}
	}
	return edpTenants
}