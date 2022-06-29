package main

import (
	"context"
	"fmt"
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

	manager := manage.NewDefaultManager()
	manager.SetAuthorizeCodeTokenCfg(manage.DefaultAuthorizeCodeTokenCfg)

	clientStore, tokenStore, err := initStores(conf.DatabaseUri)
	if err != nil {
		logrus.Fatalf("Error connecting db: %s", err.Error())
	}
	manager.MapClientStorage(clientStore)
	manager.MapTokenStorage(tokenStore)

	gateways, err := initGateways(conf)
	if err != nil {
		logrus.Fatalf("Error initializing gateways", err)
	}
	svc := &Service{
		Config:      conf,
		clientStore: clientStore,
		gateways:    gateways,
	}

	srv := server.NewServer(server.NewConfig(), manager)
	controller := &OAuthController{
		oauthServer: srv,
		service:     svc,
	}
	srv.SetUserAuthorizationHandler(controller.UserAuthorizeHandler)
	srv.SetInternalErrorHandler(controller.InternalErrorHandler)
	srv.SetAuthorizeScopeHandler(controller.AuthorizeScopeHandler)

	http.HandleFunc("/oauth/authorize", controller.AuthorizationHandler)
	http.HandleFunc("/oauth/token", controller.TokenHandler)

	//should not be publicly accesible
	http.HandleFunc("/admin/clients", controller.ClientHandler)

	//gateway
	http.HandleFunc("/v2/", controller.ApiGateway)

	logrus.Infof("Server starting on port %d", conf.Port)
	logrus.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", conf.Port), nil))
}

//hard-code lndhub origin server for now
func initGateways(conf *Config) (gateways map[string]*httputil.ReverseProxy, err error) {
	lndhubUrl, err := url.Parse(conf.LndHubUrl)
	if err != nil {
		return nil, err
	}
	return map[string]*httputil.ReverseProxy{
		"/v2/": httputil.NewSingleHostReverseProxy(lndhubUrl),
	}, nil
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
