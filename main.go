package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/jackc/pgx/v4"
	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
	"github.com/sirupsen/logrus"

	"github.com/go-oauth2/oauth2/v4/manage"
	"github.com/go-oauth2/oauth2/v4/server"
	pg "github.com/vgarvardt/go-oauth2-pg/v4"
	"github.com/vgarvardt/go-pg-adapter/pgx4adapter"
)

func main() {
	// Load env file as env variables
	err := godotenv.Load(".env")
	if err != nil {
		logrus.Errorf("Error loading environment variables: %v", err)
	}
	// Load in config from env vars
	conf := &Config{}
	err = envconfig.Process("", conf)
	if err != nil {
		logrus.Fatalf("Error loading environment variables: %v", err)
	}
	logrus.SetReportCaller(true)

	manager := manage.NewDefaultManager()
	manager.SetAuthorizeCodeTokenCfg(manage.DefaultAuthorizeCodeTokenCfg)

	clientStore, tokenStore, err := initStores(conf.DatabaseUri)
	if err != nil {
		logrus.Fatalf("Error connecting db: %s", err.Error())
	}
	manager.MapClientStorage(clientStore)
	manager.MapTokenStorage(tokenStore)

	srv := server.NewServer(server.NewConfig(), manager)
	svc := &Service{
		oauthServer: srv,
		Config:      conf,
		clientStore: clientStore,
	}
	controller := &OAuthController{
		service: svc,
	}
	srv.SetUserAuthorizationHandler(controller.UserAuthorizeHandler)
	srv.SetInternalErrorHandler(controller.InternalErrorHandler)
	srv.SetAuthorizeScopeHandler(controller.AuthorizeScopeHandler)

	http.HandleFunc("/oauth/authorize", controller.AuthorizationHandler)
	http.HandleFunc("/oauth/token", controller.TokenHandler)

	//should not be publicly accesible
	http.HandleFunc("/admin/clients", controller.ClientHandler)

	//Initialize API gateway
	gateways, err := svc.initGateways()
	if err != nil {
		logrus.Fatal(err)
	}
	for _, gw := range gateways {
		http.Handle(gw.MatchRoute, gw)
	}

	logrus.Infof("Server starting on port %d", conf.Port)
	logrus.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", conf.Port), nil))
}

func (svc *Service) initGateways() (result []*OriginServer, err error) {
	targetBytes, err := ioutil.ReadFile(svc.Config.TargetFile)
	if err != nil {
		return nil, err
	}
	result = []*OriginServer{}
	err = json.Unmarshal(targetBytes, &result)
	if err != nil {
		return nil, err
	}
	svc.scopes = map[string]bool{}
	originHelperMap := map[string]*httputil.ReverseProxy{}
	for _, origin := range result {
		origin.svc = svc
		svc.scopes[origin.Scope] = true
		//avoid creating too much identical origin server objects
		//by storing them in a map
		value, found := originHelperMap[origin.Origin]
		if found {
			//use existing one
			origin.proxy = value
		} else {
			//create new one
			originUrl, err := url.Parse(origin.Origin)
			if err != nil {
				return nil, err
			}
			proxy := httputil.NewSingleHostReverseProxy(originUrl)
			originHelperMap[origin.Origin] = proxy
			origin.proxy = proxy
		}
	}
	return result, nil
}

func initStores(db string) (clientStore *pg.ClientStore, tokenStore *pg.TokenStore, err error) {
	//connect database
	pgxConn, err := pgx.Connect(context.Background(), db)
	if err != nil {
		return nil, nil, err
	}
	// use PostgreSQL token store with pgx.Connection adapter
	adapter := pgx4adapter.NewConn(pgxConn)
	tokenStore, err = pg.NewTokenStore(adapter, pg.WithTokenStoreGCInterval(time.Minute))
	if err != nil {
		return nil, nil, err
	}
	defer tokenStore.Close()

	clientStore, err = pg.NewClientStore(adapter)
	if err != nil {
		return nil, nil, err
	}
	logrus.Info("Succesfully connected to postgres database")
	return
}
