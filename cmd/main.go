package main

import (
	"flag"
	"os"

	"github.com/docker/go-plugins-helpers/network"
	"github.com/mageshgv/docker-macvlan-noipam/driver"
	log "github.com/sirupsen/logrus"
)

var (
	logLevel = flag.String("log", "info", "log level")
	logFile  = flag.String("logfile", "", "log file")
)

func main() {
	flag.Parse()

	level, err := log.ParseLevel(*logLevel)
	if err != nil {
		log.WithError(err).Fatal("Failed to parse log level")
	}
	log.SetLevel(level)

	if *logFile != "" {
		f, err := os.OpenFile(*logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
		if err != nil {
			log.WithError(err).Fatal("Failed to open log file for writing")
		}
		defer f.Close()

		log.StandardLogger().Out = f
	}

	driver, err := driver.NewDriver()
	if err != nil {
		log.WithError(err).Fatal("Failed to create plugin")
	}

	handler := network.NewHandler(driver)
	log.Infof("Registering docker plugin")
	err = handler.ServeUnix("macvlan-noipam", 1000) // Revisit user and gid
	if err != nil {
		log.Errorf("Failed to handle docker unix api: %s", err)
	}

	// Any cleanups ?
}
