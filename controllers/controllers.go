package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"oauth2server/constants"
	"oauth2server/models"
	"oauth2server/service"
	"strings"

	oauth2gorm "github.com/getAlby/go-oauth2-gorm"
	"github.com/getsentry/sentry-go"
	oauthErrors "github.com/go-oauth2/oauth2/errors"
	"github.com/go-oauth2/oauth2/v4/errors"
	"github.com/go-oauth2/oauth2/v4/server"
	"github.com/golang-jwt/jwt"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
)

type ctx_id_type string

var CONTEXT_ID_KEY ctx_id_type = "ID"

type OAuthController struct {
	Service *service.Service
}

func (ctrl *OAuthController) AuthorizationHandler(w http.ResponseWriter, r *http.Request) {
	err := ctrl.Service.OauthServer.HandleAuthorizeRequest(w, r)
	if err != nil {
		sentry.CaptureException(err)
		http.Error(w, err.Error(), http.StatusBadRequest)
	}
}

func (ctrl *OAuthController) ScopeHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Content-type", "application/json")
	err := json.NewEncoder(w).Encode(ctrl.Service.Scopes)
	if err != nil {
		logrus.Error(err)
	}
}

func (ctrl *OAuthController) EndpointHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Content-type", "application/json")
	endpoints := ctrl.Service.Endpoints
	//not needed for clients
	for _, e := range endpoints {
		e.Origin = ""
	}
	err := json.NewEncoder(w).Encode(endpoints)
	if err != nil {
		logrus.Error(err)
	}
}

func (ctrl *OAuthController) tokenError(w http.ResponseWriter, err error) error {
	data, statusCode, header := ctrl.Service.OauthServer.GetErrorData(err)
	logrus.
		WithField("error_description", data["error_description"]).
		WithField("error", err).
		Error("token error")
	return ctrl.token(w, data, header, statusCode)
}

func (ctrl *OAuthController) token(w http.ResponseWriter, data map[string]interface{}, header http.Header, statusCode ...int) error {
	if fn := ctrl.Service.OauthServer.ResponseTokenHandler; fn != nil {
		return fn(w, data, header, statusCode...)
	}
	w.Header().Set("Content-Type", "application/json;charset=UTF-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")

	for key := range header {
		w.Header().Set(key, header.Get(key))
	}

	status := http.StatusOK
	if len(statusCode) > 0 && statusCode[0] > 0 {
		status = statusCode[0]
	}

	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(data)
}

func (ctrl *OAuthController) HandleTokenRequest(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	gt, tgr, err := ctrl.Service.OauthServer.ValidationTokenRequest(r)
	if err != nil {
		return ctrl.tokenError(w, err)
	}

	ti, err := ctrl.Service.OauthServer.GetAccessToken(ctx, gt, tgr)
	if err != nil {
		return ctrl.tokenError(w, err)
	}

	return ctrl.token(w, ctrl.Service.OauthServer.GetTokenData(ti), nil)
}

func (ctrl *OAuthController) TokenIntrospectHandler(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	tokenInfo, err := ctrl.Service.OauthServer.Manager.LoadAccessToken(r.Context(), token)
	if err != nil {
		ctrl.tokenError(w, err)
		return
	}
	// for middleware
	lti := r.Context().Value("token_info")
	if lti != nil {
		logTokenInfo := lti.(*models.LogTokenInfo)
		logTokenInfo.UserId = tokenInfo.GetUserID()
		logTokenInfo.ClientId = tokenInfo.GetClientID()
	}
	info := map[string]interface{}{
		"client_id":    tokenInfo.GetClientID(),
		"redirect_uri": tokenInfo.GetRedirectURI(),
		"scopes":       map[string]string{},
	}
	scopes := info["scopes"].(map[string]string)
	w.Header().Add("Content-type", "application/json")
	for _, sc := range strings.Split(tokenInfo.GetScope(), " ") {
		scopes[sc] = ctrl.Service.Scopes[sc]
	}
	err = json.NewEncoder(w).Encode(info)
	if err != nil {
		logrus.Error(err)
	}
}

func (ctrl *OAuthController) TokenHandler(w http.ResponseWriter, r *http.Request) {
	err := ctrl.HandleTokenRequest(w, r)
	if err != nil {
		sentry.CaptureException(err)
	}
}
func (ctrl *OAuthController) InternalErrorHandler(err error) (re *errors.Response) {
	//workaround to not show "sql: no rows in result set" to user
	sentry.CaptureException(err)
	description := oauthErrors.Descriptions[err]
	statusCode := oauthErrors.StatusCodes[err]
	if description != "" && statusCode != 0 {
		return &errors.Response{
			Error:       fmt.Errorf(description),
			ErrorCode:   statusCode,
			Description: description,
			URI:         "",
			StatusCode:  statusCode,
			Header:      map[string][]string{},
		}
	}
	return &errors.Response{
		Error:       err,
		ErrorCode:   0,
		Description: "",
		URI:         "",
		StatusCode:  500,
		Header:      map[string][]string{},
	}
}
func (ctrl *OAuthController) UserAuthorizeHandler(w http.ResponseWriter, r *http.Request) (userID string, err error) {
	token, err := ctrl.authenticateUser(r)
	if err != nil {
		logrus.Error(err)
		sentry.CaptureException(err)
		return "", err
	}

	claims := jwt.MapClaims{}
	_, err = jwt.ParseWithClaims(token, claims, func(token *jwt.Token) (interface{}, error) {
		return ctrl.Service.Config.JWTSecret, nil
	})
	if err != nil {
		return "", err
	}
	if claims["id"] == nil {
		return "", fmt.Errorf("Cannot authenticate user, token does not contain user id")
	}
	return fmt.Sprintf("%.0f", claims["id"].(float64)), nil
}

func (ctrl *OAuthController) ListAllClientsHandler(w http.ResponseWriter, r *http.Request) {
	result := []models.ClientMetaData{}
	err := ctrl.Service.DB.Find(&result, &models.ClientMetaData{}).Error
	if err != nil {
		sentry.CaptureException(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	response := []models.ListClientsResponse{}
	for _, md := range result {
		response = append(response, models.ListClientsResponse{
			ID:       md.ClientID,
			Name:     md.Name,
			ImageURL: md.ImageUrl,
			URL:      md.URL,
		})
	}
	w.Header().Add("Content-type", "application/json")
	err = json.NewEncoder(w).Encode(response)
	if err != nil {
		logrus.Error(err)
	}
}

func (ctrl *OAuthController) ListClientHandler(w http.ResponseWriter, r *http.Request) {
	userId := r.Context().Value(CONTEXT_ID_KEY)
	result := []oauth2gorm.TokenStoreItem{}
	err := ctrl.Service.DB.Table(constants.TokenTableName).Find(&result, &oauth2gorm.TokenStoreItem{
		UserID: userId.(string),
	}).Error
	if err != nil {
		sentry.CaptureException(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	response := []models.ListClientsResponse{}
	for _, ti := range result {
		//todo: more efficient queries ?
		//store information in a single relation when a token is created?
		clientMetadata := &models.ClientMetaData{}
		err = ctrl.Service.DB.First(&clientMetadata, &models.ClientMetaData{ClientID: ti.ClientID}).Error
		if err != nil {
			sentry.CaptureException(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		parsed, _ := url.Parse(ti.RedirectURI)
		scopes := map[string]string{}
		for _, sc := range strings.Split(ti.Scope, " ") {
			scopes[sc] = ctrl.Service.Scopes[sc]
		}
		response = append(response, models.ListClientsResponse{
			Domain:   parsed.Host,
			ID:       ti.ClientID,
			Name:     clientMetadata.Name,
			ImageURL: clientMetadata.ImageUrl,
			URL:      clientMetadata.URL,
			Scopes:   scopes,
		})
	}
	w.Header().Add("Content-type", "application/json")
	err = json.NewEncoder(w).Encode(response)
	if err != nil {
		logrus.Error(err)
	}
}

// should be used for budgets later
func (ctrl *OAuthController) UpdateClientHandler(w http.ResponseWriter, r *http.Request) {
}

// deletes all tokens a user currently has for a given client
func (ctrl *OAuthController) DeleteClientHandler(w http.ResponseWriter, r *http.Request) {
	clientId := mux.Vars(r)["clientId"]
	err := ctrl.Service.DB.Table(constants.TokenTableName).Delete(&oauth2gorm.TokenStoreItem{}, &oauth2gorm.TokenStoreItem{ClientID: clientId}).Error
	if err != nil {
		sentry.CaptureException(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

func (ctrl *OAuthController) UserAuthorizeMiddleware(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := ctrl.UserAuthorizeHandler(w, r)
		if err != nil {
			logrus.Errorf("Error authenticating user %s", err.Error())
			sentry.CaptureException(err)
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), CONTEXT_ID_KEY, id))
		h.ServeHTTP(w, r)
	})
}

func (ctrl *OAuthController) authenticateUser(r *http.Request) (token string, err error) {
	err = r.ParseForm()
	//look for login/password in form data
	//but also allow basic auth, form data only works with POST requests
	login, password, ok := r.BasicAuth()
	if ok {
		r.Form.Add("login", login)
		r.Form.Add("password", password)
	}
	if err != nil {
		return "", fmt.Errorf("Error parsing form data %s", err.Error())
	}
	login = r.Form.Get("login")
	password = r.Form.Get("password")

	if login == "" && password == "" {
		return "", fmt.Errorf("Cannot authenticate user, form data missing.")
	}

	if login == "" || password == "" {
		return "", fmt.Errorf("Cannot authenticate user, login or password missing.")
	}
	//authenticate user against lndhub
	resp, err := http.PostForm(fmt.Sprintf("%s/auth", ctrl.Service.Config.LndHubUrl), r.Form)
	if err != nil {
		return "", fmt.Errorf("Error authenticating user %s", err.Error())
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Cannot authenticate user, login or password wrong.")
	}
	//return access code
	tokenResponse := &TokenResponse{}
	err = json.NewDecoder(resp.Body).Decode(tokenResponse)
	if err != nil {
		return "", fmt.Errorf("Error authenticating user %s", err.Error())
	}
	return tokenResponse.AccessToken, nil
}

func (ctrl *OAuthController) PreRedirectErrorHandler(w http.ResponseWriter, r *server.AuthorizeRequest, err error) error {
	logrus.WithField("Authorize request", r).Error(err)
	sentry.CaptureException(err)
	return err
}

func (ctrl *OAuthController) AuthorizeScopeHandler(w http.ResponseWriter, r *http.Request) (scope string, err error) {
	requestedScope := r.FormValue("scope")
	if requestedScope == "" {
		return "", fmt.Errorf("Empty scope is not allowed")
	}
	for _, scope := range strings.Split(requestedScope, " ") {
		if _, found := ctrl.Service.Scopes[scope]; !found {
			err = fmt.Errorf("Scope not allowed: %s", scope)
			sentry.CaptureException(err)
			return "", err
		}
	}
	return requestedScope, nil
}

type TokenResponse struct {
	AccessToken string `json:"access_token"`
}
