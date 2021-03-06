package main

import (
	"database/sql"
	"errors"
	"flag"
	"os"

	"github.com/fritzpay/paymentd/pkg/config"
	"github.com/fritzpay/paymentd/pkg/env"
	"github.com/fritzpay/paymentd/pkg/server"
	"github.com/fritzpay/paymentd/pkg/service"
	"github.com/fritzpay/paymentd/pkg/service/api"
	"github.com/fritzpay/paymentd/pkg/service/web"
	_ "github.com/go-sql-driver/mysql"
	"golang.org/x/net/context"
	"gopkg.in/inconshreveable/log15.v2"
)

const (
	// AppName is the name of the application
	AppName = "paymentd"
	// AppVersion is the version of the application
	AppVersion = "0.9.0-alpha"
)

// command line flags
var (
	// cfgFileName is the configuration file to use
	cfgFileName string
)

const (
	envVarConfigFileName = "PAYMENTDCFG"
)

var (
	log    log15.Logger
	cfg    config.Config
	srv    *server.Server
	ctx    context.Context
	cancel context.CancelFunc
)

func main() {
	// set flags
	flag.StringVar(&cfgFileName, "c", "", "config file name to use")
	flag.Parse()

	setEnv()

	log = env.Log.New(log15.Ctx{
		"AppName":    AppName,
		"AppVersion": AppVersion,
		"PID":        os.Getpid(),
	})
	log.Info("starting daemon...")

	log.Info("loading config...")
	loadConfig()

	// initialize root context
	ctx, cancel = context.WithCancel(context.Background())
	ctx = context.WithValue(ctx, "log", log)

	log.Info("initializing server...")
	srv = server.NewServer(ctx)
	srv.Cancel = cancel

	// services
	log.Info("initializing service context...")
	serviceCtx, err := service.NewContext(ctx, cfg, log)
	if err != nil {
		log.Crit("error initializing service context", log15.Ctx{"err": err})
		log.Info("exiting...")
		os.Exit(1)
	}
	// database
	log.Info("connecting databases...")
	err = connectDB(serviceCtx)
	if err != nil {
		log.Crit("error connecting databases", log15.Ctx{"err": err})
		log.Info("exiting...")
		os.Exit(1)
	}

	log.Info("setting payment defaults...")
	err = setDefaults(serviceCtx)
	if err != nil {
		log.Crit("error on setting payment defaults", log15.Ctx{"err": err})
		log.Info("exiting...")
		os.Exit(1)
	}

	// API handler
	if cfg.API.Active {
		log.Info("enabling API service...")
		apiHandler, err := api.NewHandler(serviceCtx)
		if err != nil {
			log.Crit("error initializing API service", log15.Ctx{"err": err})
			log.Info("exiting...")
			os.Exit(1)
		}
		err = srv.RegisterService(cfg.API.Service, apiHandler)
		if err != nil {
			log.Crit("error registering API service", log15.Ctx{"err": err})
			log.Info("exiting...")
			os.Exit(1)
		}
	}
	if cfg.Web.Active {
		log.Info("enabling Web service...")
		webHandler, err := web.NewHandler(serviceCtx)
		if err != nil {
			log.Crit("error initializing Web service", log15.Ctx{"err": err})
			log.Info("exiting...")
			os.Exit(1)
		}
		err = srv.RegisterService(cfg.Web.Service, webHandler)
		if err != nil {
			log.Crit("error registering Web service", log15.Ctx{"err": err})
			log.Info("exiting...")
			os.Exit(1)
		}
	}

	log.Info("serving...")
	err = srv.Serve()
	if err != nil {
		log.Crit("error serving", log15.Ctx{"err": err})
		log.Info("exiting...")
		os.Exit(1)
	}
}

func loadConfig() {
	cfg = config.DefaultConfig()
	if cfgFileName == "" && os.Getenv(envVarConfigFileName) != "" {
		cfgFileName = os.Getenv(envVarConfigFileName)
		log.Info("using config file name from env", log15.Ctx{
			"envVar":      envVarConfigFileName,
			"cfgFileName": cfgFileName,
		})
	}
	if cfgFileName == "" {
		log.Info("no config file provided. trying default config...")
	} else {
		log.Info("opening config file...", log15.Ctx{"cfgFileName": cfgFileName})
		cfgFile, err := os.Open(cfgFileName)
		if err != nil {
			log.Crit("could not open config file", log15.Ctx{"cfgFileName": cfgFileName})
			log.Info("exiting...")
			os.Exit(1)
		}
		err = (&cfg).ReadConfig(cfgFile)
		if err != nil {
			log.Crit("error reading config file", log15.Ctx{"cfgFileName": cfgFileName, "err": err})
			log.Info("exiting...")
			os.Exit(1)
		}
		err = cfgFile.Close()
		if err != nil {
			log.Crit("error closing config file", log15.Ctx{"cfgFileName": cfgFileName, "err": err})
			log.Info("exiting...")
			os.Exit(1)
		}
	}
}

func connectDB(ctx *service.Context) error {
	if cfg.Database.Principal.Write == nil {
		return errors.New("principal write DB config error")
	}
	principalDBW, err := sql.Open(cfg.Database.Principal.Write.Type(), cfg.Database.Principal.Write.DSN())
	if err != nil {
		return err
	}
	principalDBW.SetMaxOpenConns(cfg.Database.MaxOpenConns)
	principalDBW.SetMaxIdleConns(cfg.Database.MaxIdleConns)
	var principalDBRO *sql.DB
	if cfg.Database.Principal.ReadOnly != nil {
		principalDBRO, err = sql.Open(cfg.Database.Principal.ReadOnly.Type(), cfg.Database.Principal.ReadOnly.DSN())
		if err != nil {
			return err
		}
		principalDBRO.SetMaxOpenConns(cfg.Database.MaxOpenConns)
		principalDBRO.SetMaxIdleConns(cfg.Database.MaxIdleConns)
	}
	ctx.SetPrincipalDB(principalDBW, principalDBRO)

	if cfg.Database.Payment.Write == nil {
		return errors.New("payment write DB config error")
	}
	paymentDBW, err := sql.Open(cfg.Database.Payment.Write.Type(), cfg.Database.Payment.Write.DSN())
	if err != nil {
		return err
	}
	paymentDBW.SetMaxOpenConns(cfg.Database.MaxOpenConns)
	paymentDBW.SetMaxIdleConns(cfg.Database.MaxIdleConns)
	var paymentDBRO *sql.DB
	if cfg.Database.Payment.ReadOnly != nil {
		paymentDBRO, err = sql.Open(cfg.Database.Payment.ReadOnly.Type(), cfg.Database.Payment.ReadOnly.DSN())
		if err != nil {
			return err
		}
		paymentDBRO.SetMaxOpenConns(cfg.Database.MaxOpenConns)
		paymentDBRO.SetMaxIdleConns(cfg.Database.MaxIdleConns)
	}
	ctx.SetPaymentDB(paymentDBW, paymentDBRO)

	return nil
}
