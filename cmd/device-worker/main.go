package main

import (
	"context"
	"fmt"
	"github.com/jakub-dzon/k4e-device-worker/internal/metrics"

	configuration2 "github.com/jakub-dzon/k4e-device-worker/internal/configuration"
	"github.com/jakub-dzon/k4e-device-worker/internal/datatransfer"
	hardware2 "github.com/jakub-dzon/k4e-device-worker/internal/hardware"
	heartbeat2 "github.com/jakub-dzon/k4e-device-worker/internal/heartbeat"
	os2 "github.com/jakub-dzon/k4e-device-worker/internal/os"
	registration2 "github.com/jakub-dzon/k4e-device-worker/internal/registration"
	"github.com/jakub-dzon/k4e-device-worker/internal/server"
	workload2 "github.com/jakub-dzon/k4e-device-worker/internal/workload"

	"net"
	"os"
	"path"
	"time"

	"git.sr.ht/~spc/go-log"

	pb "github.com/redhatinsights/yggdrasil/protocol"
	"google.golang.org/grpc"
)

var yggdDispatchSocketAddr string

const (
	defaultDataDir = "/var/local/yggdrasil"
	metricsEnabled = true
)

func main() {
	log.SetFlags(0) // No datatime, is already done on yggradsil server

	logLevel, ok := os.LookupEnv("LOG_LEVEL")
	if !ok {
		logLevel = "ERROR"
	}
	level, err := log.ParseLevel(logLevel)
	if err != nil {
		level = log.LevelError
	}
	log.SetLevel(level)
	// Get initialization values from the environment.
	yggdDispatchSocketAddr, ok = os.LookupEnv("YGG_SOCKET_ADDR")
	if !ok {
		log.Fatal("missing YGG_SOCKET_ADDR environment variable")
	}

	baseDataDir, ok := os.LookupEnv("BASE_DATA_DIR")
	if !ok {
		log.Warnf("missing BASE_DATA_DIR environment variable. Using default: %s", defaultDataDir)
		baseDataDir = defaultDataDir
	}

	// Dial the dispatcher on its well-known address.
	conn, err := grpc.Dial(yggdDispatchSocketAddr, grpc.WithInsecure())
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	// Create a dispatcher client
	dispatcherClient := pb.NewDispatcherClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	// Register as a handler of the "device" type.
	r, err := dispatcherClient.Register(ctx, &pb.RegistrationRequest{Handler: "device", Pid: int64(os.Getpid())})
	if err != nil {
		log.Fatal(err)
	}
	if !r.GetRegistered() {
		log.Fatal("handler registration failed")
	}

	// Listen on the provided socket address.
	l, err := net.Listen("unix", r.GetAddress())
	if err != nil {
		log.Fatalf("cannot start listening on %s err: %v", r.GetAddress(), err)
	}

	// Register as a Worker service with gRPC and start accepting connections.
	dataDir := path.Join(baseDataDir, "device")
	log.Infof("Data directory: %s", dataDir)
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		log.Fatal(fmt.Errorf("cannot create directory: %w", err))
	}
	deviceId, ok := os.LookupEnv("DEVICE_ID")
	if !ok {
		log.Warn("DEVICE_ID environment variable has not been set")
		deviceId = "unknown"
	}
	configManager := configuration2.NewConfigurationManager(dataDir)

	wl, err := workload2.NewWorkloadManager(dataDir, deviceId)
	if err != nil {
		log.Fatalf("cannot start Workload Manager. DeviceID: %s; err: %v", deviceId, err)
	}
	configManager.RegisterObserver(wl)

	hw := hardware2.Hardware{}

	dataMonitor := datatransfer.NewMonitor(wl, configManager)
	wl.RegisterObserver(dataMonitor)
	configManager.RegisterObserver(dataMonitor)
	dataMonitor.Start()
	metricsStore, err := metrics.NewMetrics(dataDir)
	if err != nil {
		log.Fatalf("cannot start metrics store. DeviceID: %s; err: %v", deviceId, err)
	}
	monitor := metrics.NewMonitor(metricsStore)
	go monitor.LogCurrentMetrics(true)
	go metrics.NewGenerator(metricsStore).GenerateRandom("tester", 5*time.Second)
	hbs := heartbeat2.NewHeartbeatService(dispatcherClient, configManager, wl, &hw, dataMonitor, metricsStore)

	configManager.RegisterObserver(hbs)

	deviceOs := os2.OS{}
	reg := registration2.NewRegistration(&hw, &deviceOs, dispatcherClient, configManager, hbs, wl, dataMonitor)

	s := grpc.NewServer()
	pb.RegisterWorkerServer(s, server.NewDeviceServer(configManager, reg))
	if !configManager.IsInitialConfig() {
		hbs.Start()
	} else {
		reg.RegisterDevice()
	}

	if err := s.Serve(l); err != nil {
		log.Fatalf("cannot start worker server, err: %v", err)
	}
}
