package filters

import (
	ctx "context"
	appCtx "edp-admin-console/context"
	"edp-admin-console/util"
	"encoding/json"
	"github.com/astaxie/beego"
	bgCtx "github.com/astaxie/beego/context"
	"github.com/coreos/go-oidc"
	"github.com/satori/go.uuid"
	"golang.org/x/oauth2"
	"log"
	"net/http"
)

func AuthFilter(context *bgCtx.Context) {
	log.Println("Start auth filter..")
	rawToken := context.Input.Session("token")
	if rawToken == nil {
		log.Println("There are no token in the session")
		startAuth(context)
		return
	}
	token := rawToken.(*oauth2.Token)
	idToken, err := appCtx.GetAuthConfig().Verifier.Verify(ctx.Background(), token.AccessToken)
	if err != nil {
		log.Printf("Token presented in the session is not valid")
		startAuth(context)
		return
	}
	realmRoles := getRealmRoles(context, idToken)
	log.Printf("Roles %s has been retrieved from the token", realmRoles)
	resourceRoles := getResourceAccessValues(context, idToken)
	log.Printf("ResourceAccess %s has been retrieved from the token", resourceRoles)
	context.Output.Session("resource_access", resourceRoles)
}

func CheckEDPTenantRole(context *bgCtx.Context) {
	edpName := context.Input.Param(":name")

	resourceAccess := context.Input.Session("resource_access").(map[string][]string)
	if val, ok := resourceAccess[edpName+"-edp"]; ok {
		if !util.Contains(val, beego.AppConfig.String("adminRole")) {
			http.Error(context.ResponseWriter, "Forbidden.", http.StatusForbidden)
		}
	} else {
		http.Error(context.ResponseWriter, "Resource doesn't exist.", http.StatusNotFound)
	}
}

func getRealmRoles(context *bgCtx.Context, token *oidc.IDToken) []string {
	log.Printf("Start to check roles ...")
	var claim map[string]*json.RawMessage
	err := token.Claims(&claim)
	if err != nil {
		log.Printf("Error has been occurred during the parsing token %+v", token)
		http.Error(context.ResponseWriter, "Internal Error", http.StatusInternalServerError)
	}
	var realmAccess map[string]*[]string
	err = json.Unmarshal(*claim["realm_access"], &realmAccess)
	if err != nil {
		log.Printf("Error has been occurred during the parsing token %+v", token)
		http.Error(context.ResponseWriter, "Internal Error", http.StatusInternalServerError)
	}

	return *realmAccess["roles"]
}

func getResourceAccessValues(context *bgCtx.Context, token *oidc.IDToken) map[string][]string {
	log.Printf("Start to check roles ...")
	var claim map[string]*json.RawMessage
	err := token.Claims(&claim)
	if err != nil {
		log.Printf("Error has been occurred during the parsing token %+v", token)
		http.Error(context.ResponseWriter, "Internal Error", http.StatusInternalServerError)
	}
	var resourceAccess map[string]*map[string][]string
	err = json.Unmarshal(*claim["resource_access"], &resourceAccess)
	if err != nil {
		log.Printf("Error has been occurred during the parsing token %+v", token)
		http.Error(context.ResponseWriter, "Internal Error", http.StatusInternalServerError)
	}

	instances := make(map[string][]string, len(resourceAccess))
	for key, value := range resourceAccess {
		var val = *value
		instances[key] = val["roles"]
	}
	return instances
}

func startAuth(context *bgCtx.Context) {
	authConfig := appCtx.GetAuthConfig()
	state := uuid.NewV4().String()
	log.Printf("State %s has been generated, saved in the session and added in the auth request", state)
	context.Output.Session(authConfig.StateAuthKey, state)
	context.Redirect(http.StatusFound, authConfig.Oauth2Config.AuthCodeURL(state))
}