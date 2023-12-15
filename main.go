package main

import (
	"context"
	"os"
	"os/signal"
	"scratchdata/cmd"
	"scratchdata/cmd/api"
	"scratchdata/config"
	"scratchdata/pkg/database"
	"scratchdata/pkg/filestore"
	memorystore "scratchdata/pkg/filestore/memory"
	"scratchdata/pkg/queue"
	memoryqueue "scratchdata/pkg/queue/memory"
	"scratchdata/pkg/transport/queuestorage"
	"strconv"
	"syscall"

	"github.com/BurntSushi/toml"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func setupLogs(logConfig config.Logs) {
	// Equivalent of Lshortfile
	zerolog.CallerMarshalFunc = func(pc uintptr, file string, line int) string {
		short := file
		for i := len(file) - 1; i > 0; i-- {
			if file[i] == '/' {
				short = file[i+1:]
				break
			}
		}
		file = short
		return file + ":" + strconv.Itoa(line)
	}

	// Set log level
	zerolog.SetGlobalLevel(logConfig.ToLevel())

	// Set log output format
	if logConfig.Pretty {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr}).With().Caller().Logger()
	} else {
		log.Logger = log.With().Caller().Logger()
	}
}

func getConfig(filePath string) config.Config {
	var conf config.Config
	if _, err := toml.DecodeFile(filePath, &conf); err != nil {
		log.Fatal().Err(err).Msg("Unable to load config file")
	}
	return conf
}

func main() {
	configFile := os.Args[1]
	config := getConfig(configFile)

	setupLogs(config.Logs)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var db database.Database
	db = database.GetDB(config.Database)

	err := db.Open()
	if err != nil {
		log.Fatal().Err(err).Msg("Unable to connect to database")
	}
	defer db.Close()

	var queueBackend queue.QueueBackend
	switch config.Transport.Queue {
	case "memory":
		queueBackend = memoryqueue.NewQueue()
	case "sqs":
		// TODO: replace with SQS queue once it's implemented
		queueBackend = memoryqueue.NewQueue()
	default:
		log.Fatal().Msgf("Invalid transport.queue: %s: Expected 'memory' or 'sqs'", config.Transport.Queue)
	}

	var storageBackend filestore.StorageBackend
	switch config.Transport.Storage {
	case "memory":
		storageBackend = memorystore.NewStorage()
	case "s3":
		// TODO: replace with S3 storage once it's implemented
		storageBackend = memorystore.NewStorage()
	default:
		log.Fatal().Msgf("Invalid transport.storage: %s: Expected 'memory' or 's3'", config.Transport.Storage)
	}

	dataTransport := queuestorage.NewQueueStorageTransport(queueBackend, storageBackend)

	// go dataTransport.StartProducer()
	go dataTransport.StartConsumer()

	commands := make([]cmd.Command, 0)
	if config.API.Enabled {
		commands = append(commands, api.NewAPIServer(config.API, db, dataTransport))
	}

	if len(commands) == 0 {
		log.Fatal().Msg("No services are enabled in config file")
	}

	for _, command := range commands {
		go func(command cmd.Command) {
			err := command.Start()
			if err != nil {
				log.Fatal().Err(err).Msg("Failed to start service")
			}
		}(command)
	}

	select {
	case <-ctx.Done():
		for _, command := range commands {
			command.Stop()
		}

		// dataTransport.StopProducer()
		dataTransport.StopConsumer()
	}
}
