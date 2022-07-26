package main

import (
	"fmt"
	"net/http"
	"oauth2server/constants"
	"oauth2server/controllers"
	"oauth2server/models"
	"oauth2server/service"
	"os"
	"time"

	"github.com/gorilla/mux"
	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
	"github.com/sirupsen/logrus"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	oauth2gorm "github.com/getAlby/go-oauth2-gorm"
	"github.com/getsentry/sentry-go"
	sentryhttp "github.com/getsentry/sentry-go/http"
	"github.com/go-oauth2/oauth2/v4/manage"
	"github.com/go-oauth2/oauth2/v4/server"
	"github.com/gorilla/handlers"
)

func main() {
	// Load env file as env variables
	err := godotenv.Load(".env")
	if err != nil {
		logrus.Errorf("Error loading environment variables: %v", err)
	}
	// Load in config from env vars
	conf := &service.Config{}
	err = envconfig.Process("", conf)
	if err != nil {
		logrus.Fatalf("Error loading environment variables: %v", err)
	}
	logrus.SetReportCaller(true)

	// Setup exception tracking with Sentry if configured
	if conf.SentryDSN != "" {
		if err = sentry.Init(sentry.ClientOptions{
			Dsn:          conf.SentryDSN,
			IgnoreErrors: []string{"401"},
		}); err != nil {
			logrus.Errorf("sentry init error: %v", err)
		}
		defer sentry.Flush(2 * time.Second)
	}

	manager := manage.NewDefaultManager()
	manager.SetAuthorizeCodeTokenCfg(manage.DefaultAuthorizeCodeTokenCfg)

	clientStore, tokenStore, db, err := initStores(conf.DatabaseUri)
	if err != nil {
		logrus.Fatalf("Error connecting db: %s", err.Error())
	}

	//initialize extra db tables
	err = db.AutoMigrate(&models.ClientMetaData{})
	if err != nil {
		logrus.Fatalf("Error connecting db: %s", err.Error())
	}

	manager.MapClientStorage(clientStore)
	manager.MapTokenStorage(tokenStore)

	manager.SetValidateURIHandler(controllers.CheckRedirectUriDomain)

	manager.SetAuthorizeCodeTokenCfg(&manage.Config{
		AccessTokenExp:    time.Duration(conf.AccessTokenExpSeconds) * time.Second,
		RefreshTokenExp:   time.Duration(conf.RefreshTokenExpSeconds) * time.Second,
		IsGenerateRefresh: true,
	})

	srv := server.NewServer(server.NewConfig(), manager)
	svc := &service.Service{
		DB:          db,
		OauthServer: srv,
		Config:      conf,
		ClientStore: clientStore,
	}
	controller := &controllers.OAuthController{
		Service: svc,
	}
	srv.SetUserAuthorizationHandler(controller.UserAuthorizeHandler)
	srv.SetInternalErrorHandler(controller.InternalErrorHandler)
	srv.SetAuthorizeScopeHandler(controller.AuthorizeScopeHandler)

	r := mux.NewRouter()
	r.HandleFunc("/oauth/authorize", controller.AuthorizationHandler)
	r.HandleFunc("/oauth/token", controller.TokenHandler)
	r.HandleFunc("/oauth/scopes", controller.ScopeHandler)

	//should not be publicly accesible
	r.HandleFunc("/admin/clients", controller.CreateClientHandler).Methods(http.MethodPost)

	//manages connected apps for users
	subRouter := r.Methods(http.MethodGet, http.MethodPost, http.MethodDelete).Subrouter()
	subRouter.HandleFunc("/clients", controller.ListClientHandler).Methods(http.MethodGet)
	subRouter.HandleFunc("/clients/{clientId}", controller.UpdateClientHandler).Methods(http.MethodPost)
	subRouter.HandleFunc("/clients/{clientId}", controller.DeleteClientHandler).Methods(http.MethodDelete)
	subRouter.Use(controller.UserAuthorizeMiddleware)

	//Initialize API gateway
	gateways, err := svc.InitGateways()
	if err != nil {
		logrus.Fatal(err)
	}
	for _, gw := range gateways {
		r.Handle(gw.MatchRoute, gw)
	}

	logrus.Infof("Server starting on port %d", conf.Port)
	logrus.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", conf.Port), registerMiddleware(r)))
}

//panic recover, logging, Sentry middlewares
func registerMiddleware(in http.Handler) http.Handler {
	recoveryHandler := handlers.RecoveryHandler()(in)
	loggingHandler := handlers.CombinedLoggingHandler(os.Stdout, recoveryHandler)
	result := sentryhttp.New(sentryhttp.Options{}).Handle(loggingHandler)
	return result
}
func initStores(dsn string) (clientStore *oauth2gorm.ClientStore, tokenStore *oauth2gorm.TokenStore, db *gorm.DB, err error) {
	//connect database
	db, err = gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, nil, nil, err
	}
	//migrated from legacy tables
	err = db.Table(constants.ClientTableName).AutoMigrate(&oauth2gorm.ClientStoreItem{})
	if err != nil {
		return nil, nil, nil, err
	}
	err = db.Table(constants.TokenTableName).AutoMigrate(&oauth2gorm.TokenStoreItem{})
	if err != nil {
		return nil, nil, nil, err
	}
	tokenStore = oauth2gorm.NewTokenStoreWithDB(&oauth2gorm.Config{TableName: constants.TokenTableName}, db, constants.GCIntervalSeconds)
	clientStore = oauth2gorm.NewClientStoreWithDB(&oauth2gorm.Config{TableName: constants.ClientTableName}, db)

	logrus.Info("Succesfully connected to postgres database")
	return
}
