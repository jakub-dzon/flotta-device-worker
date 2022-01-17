package metrics

import (
	"github.com/jakub-dzon/k4e-device-worker/internal/configuration"
	"github.com/jakub-dzon/k4e-operator/models"
	"reflect"
	"sync/atomic"
	"time"
)

const (
	systemTargetName                     = "system"
	DefaultSystemMetricsScrapingInterval = 60
	NodeExporterMetricsEndpoint          = "http://localhost:9100/metrics"
)

var defaultSystemMetricsConfiguration = models.SystemMetricsConfiguration{Interval: DefaultSystemMetricsScrapingInterval}

//go:generate mockgen -package=metrics -destination=configuration_provider_mock.go . DeviceConfigurationProvider
type DeviceConfigurationProvider interface {
	GetDeviceConfiguration() models.DeviceConfiguration
}

type SystemMetrics struct {
	latestConfig atomic.Value
	daemon       MetricsDaemon
	config       configuration.Manager
}

func NewSystemMetrics(daemon MetricsDaemon, config DeviceConfigurationProvider) *SystemMetrics {
	expectedConfiguration := expectedConfiguration(config.GetDeviceConfiguration())

	sm := &SystemMetrics{
		daemon: daemon,
	}
	daemon.AddTarget("system", []string{NodeExporterMetricsEndpoint}, time.Duration(expectedConfiguration.Interval)*time.Second, nil)
	sm.latestConfig.Store(&expectedConfiguration)
	return sm
}

func (sm *SystemMetrics) Update(config models.DeviceConfigurationMessage) error {
	newConfiguration := expectedConfiguration(*config.Configuration)
	latestConfig := sm.latestConfig.Load()
	if latestConfig != nil {
		oldConfiguration := latestConfig.(*models.SystemMetricsConfiguration)
		if oldConfiguration != nil && reflect.DeepEqual(newConfiguration, *oldConfiguration) {
			return nil
		}
	}

	sm.daemon.AddTarget(systemTargetName, []string{NodeExporterMetricsEndpoint}, time.Duration(newConfiguration.Interval)*time.Second, nil)
	sm.latestConfig.Store(&newConfiguration)

	return nil
}

func expectedConfiguration(config models.DeviceConfiguration) models.SystemMetricsConfiguration {
	newConfiguration := defaultSystemMetricsConfiguration
	if config.Metrics != nil && config.Metrics.System != nil {
		newConfiguration = *config.Metrics.System
	}
	return newConfiguration
}