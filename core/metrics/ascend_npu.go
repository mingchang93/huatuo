// Copyright 2026 The HuaTuo Authors
// Copyright 2026 The Ascend Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package collector

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"huatuo-bamai/core/metrics/ascend/dcmi"
	"huatuo-bamai/internal/log"
	"huatuo-bamai/pkg/metric"
	"huatuo-bamai/pkg/tracing"
	"huatuo-bamai/pkg/types"
)

func init() {
	tracing.RegisterEventTracing("ascend_npu", newAscendNpuCollector)
}

type ascendNpuCollector struct{}

func newAscendNpuCollector() (*tracing.EventTracingAttr, error) {
	if err := dcmi.DcInit(); err != nil {
		log.Errorf("ascend_npu: DcInit failed: %v", err)
		return nil, types.ErrNotSupported
	}

	return &tracing.EventTracingAttr{
		TracingData: &ascendNpuCollector{},
		Flag:        tracing.FlagMetric,
	}, nil
}

func (a *ascendNpuCollector) Update() ([]*metric.Data, error) {
	ctx := context.Background()
	metrics, err := ascendCollectMetrics(ctx)
	if err != nil {
		var dcmiErr *dcmi.Error
		if ok := errors.As(err, &dcmiErr); ok {
			log.Errorf("ascend_npu: dcmi error, re-initing and retrying: %v", err)

			if err := dcmi.DcInit(); err != nil {
				return nil, fmt.Errorf("failed to re-init dcmi: %w", err)
			}
			return ascendCollectMetrics(ctx)
		}

		return nil, err
	}

	return metrics, nil
}

func ascendCollectMetrics(ctx context.Context) ([]*metric.Data, error) {
	var metrics []*metric.Data

	_, cardList, err := dcmi.DcGetCardList(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get card list: %w", err)
	}

	for _, cardId := range cardList {
		deviceNum, err := dcmi.DcGetDeviceNumInCard(ctx, cardId)
		if err != nil {
			return nil, fmt.Errorf("failed to get device count for card %d: %w", cardId, err)
		}

		for deviceId := int32(0); deviceId < deviceNum; deviceId++ {
			npuMetrics, err := ascendCollectNpuMetrics(ctx, uint32(cardId), uint32(deviceId))
			if err != nil {
				return nil, fmt.Errorf("failed to collect npu metrics for card %d device %d: %w",
					cardId, deviceId, err)
			}
			metrics = append(metrics, npuMetrics...)
		}
	}

	return metrics, nil
}

func ascendCollectNpuMetrics(ctx context.Context, cardId, deviceId uint32) ([]*metric.Data, error) {
	var metrics []*metric.Data

	// Device health
	health, err := dcmi.DcGetDeviceHealth(ctx, cardId, deviceId)
	if err != nil {
		return nil, fmt.Errorf("failed to get device health: %w", err)
	}
	metrics = append(metrics,
		metric.NewGaugeData("npu_device_health", float64(health),
			"NPU device health status. 0: normal, 1: warning, 2: major, 3: critical, 0xFFFFFFFF: device missing.",
			map[string]string{
				"card":   strconv.Itoa(int(cardId)),
				"device": strconv.Itoa(int(deviceId)),
			}),
	)

	// Device power
	power, err := dcmi.DcGetDevicePowerInfo(ctx, cardId, deviceId)
	if err != nil {
		return nil, fmt.Errorf("failed to get device power: %w", err)
	}
	metrics = append(metrics,
		metric.NewGaugeData("npu_power", float64(power),
			"NPU device power consumption in watts.",
			map[string]string{
				"card":   strconv.Itoa(int(cardId)),
				"device": strconv.Itoa(int(deviceId)),
			}),
	)

	// Device temperature
	temp, err := dcmi.DcGetDeviceTemperature(ctx, cardId, deviceId)
	if err != nil {
		return nil, fmt.Errorf("failed to get device temperature: %w", err)
	}
	metrics = append(metrics,
		metric.NewGaugeData("npu_temperature", float64(temp),
			"NPU device temperature in degrees Celsius.",
			map[string]string{
				"card":   strconv.Itoa(int(cardId)),
				"device": strconv.Itoa(int(deviceId)),
			}),
	)

	// Device voltage
	voltage, err := dcmi.DcGetDeviceVoltage(ctx, cardId, deviceId)
	if err != nil {
		return nil, fmt.Errorf("failed to get device voltage: %w", err)
	}
	metrics = append(metrics,
		metric.NewGaugeData("npu_voltage", float64(voltage),
			"NPU device voltage in volts.",
			map[string]string{
				"card":   strconv.Itoa(int(cardId)),
				"device": strconv.Itoa(int(deviceId)),
			}),
	)

	// Device utilization rates (same DCMI function, different input_type)
	utilTypes := []struct {
		metricName string
		devType    dcmi.DeviceType
	}{
		{"npu_util_rate_hbm", dcmi.DeviceTypeHBM},
		{"npu_util_rate_ai_core", dcmi.DeviceTypeAICore},
		{"npu_util_rate_vector_core", dcmi.DeviceTypeVectorCore},
		{"npu_util_rate_ai_cpu", dcmi.DeviceTypeAICPU},
		{"npu_util_rate_ctrl_cpu", dcmi.DeviceTypeCtrlCPU},
	}
	for _, ut := range utilTypes {
		rate, err := dcmi.DcGetDeviceUtilizationRate(ctx, cardId, deviceId, ut.devType)
		if err != nil {
			log.Debugf("ascend: utilization %s for card %d device %d failed: %v", ut.devType.Name, cardId, deviceId, err)
			continue
		}
		metrics = append(metrics,
			metric.NewGaugeData(ut.metricName, float64(rate),
				"NPU device utilization rate (0-100%).",
				map[string]string{
					"card":   strconv.Itoa(int(cardId)),
					"device": strconv.Itoa(int(deviceId)),
				}),
		)
	}

	// Device frequencies (same DCMI function, different freq_type)
	freqTypes := []struct {
		metricName string
		devType    dcmi.DeviceType
	}{
		{"npu_freq_ai_core", dcmi.FreqTypeAICore},
		{"npu_freq_ctrl_cpu", dcmi.FreqTypeCtrlCPU},
		{"npu_freq_ai_core_rated", dcmi.FreqTypeAICoreRated},
	}
	for _, ft := range freqTypes {
		freq, err := dcmi.DcGetDeviceFrequency(ctx, cardId, deviceId, ft.devType)
		if err != nil {
			log.Debugf("ascend: frequency %s for card %d device %d failed: %v", ft.devType.Name, cardId, deviceId, err)
			continue
		}
		metrics = append(metrics,
			metric.NewGaugeData(ft.metricName, float64(freq),
				"NPU device frequency in MHz.",
				map[string]string{
					"card":   strconv.Itoa(int(cardId)),
					"device": strconv.Itoa(int(deviceId)),
				}),
		)
	}

	// Device network health
	if netHealth, err := dcmi.DcGetDeviceNetWorkHealth(ctx, cardId, deviceId); err != nil {
		log.Debugf("ascend: network health for card %d device %d failed: %v", cardId, deviceId, err)
	} else {
		metrics = append(metrics,
		metric.NewGaugeData("npu_device_network_health", float64(netHealth),
			"NPU device network health. 0: normal, 1: socket create failed, 2: rx timeout, 3: ip unreachable, 4: probe timeout, 5: probe send failed, 6: probe init, 7: probe create failed, 8: setting probe ip.",
			map[string]string{
				"card":   strconv.Itoa(int(cardId)),
				"device": strconv.Itoa(int(deviceId)),
			}),
	)
	}

	npuLabels := map[string]string{
		"card":   strconv.Itoa(int(cardId)),
		"device": strconv.Itoa(int(deviceId)),
	}

	// Device HBM info
	if hbmInfo, err := dcmi.DcGetHbmInfo(ctx, cardId, deviceId); err != nil {
		log.Debugf("ascend: HBM info for card %d device %d failed: %v", cardId, deviceId, err)
	} else {
	metrics = append(metrics,
		metric.NewGaugeData("npu_hbm_mem_capacity", float64(hbmInfo.MemorySize), "NPU HBM memory capacity in MB.", npuLabels),
		metric.NewGaugeData("npu_hbm_freq", float64(hbmInfo.Frequency), "NPU HBM frequency in MHz.", npuLabels),
		metric.NewGaugeData("npu_freq_hbm", float64(hbmInfo.Frequency), "NPU HBM frequency in MHz.", npuLabels),
		metric.NewGaugeData("npu_hbm_usage", float64(hbmInfo.Usage), "NPU HBM memory usage in MB.", npuLabels),
		metric.NewGaugeData("npu_hbm_temperature", float64(hbmInfo.Temp), "NPU HBM temperature in degrees Celsius.", npuLabels),
		metric.NewGaugeData("npu_hbm_bandwidth_util", float64(hbmInfo.BandWidthUtilRate), "NPU HBM bandwidth utilization (%).", npuLabels),
		metric.NewGaugeData("npu_util_rate_hbm_bw", float64(hbmInfo.BandWidthUtilRate), "NPU HBM bandwidth utilization (%).", npuLabels),
	)
	}

	// Device ECC info (HBM)
	if eccInfo, err := dcmi.DcGetDeviceEccInfo(ctx, cardId, deviceId, dcmi.DcmiDeviceTypeHBM); err != nil {
		log.Debugf("ascend: ECC info for card %d device %d failed: %v", cardId, deviceId, err)
	} else {
	metrics = append(metrics,
		metric.NewGaugeData("npu_hbm_ecc_enable", float64(eccInfo.EnableFlag), "NPU HBM ECC enable flag.", npuLabels),
		metric.NewCounterData("npu_hbm_single_bit_error_cnt", float64(eccInfo.SingleBitErrorCnt), "NPU HBM current single-bit error count.", npuLabels),
		metric.NewCounterData("npu_hbm_double_bit_error_cnt", float64(eccInfo.DoubleBitErrorCnt), "NPU HBM current double-bit error count.", npuLabels),
		metric.NewCounterData("npu_hbm_total_single_bit_error_cnt", float64(eccInfo.TotalSingleBitErrorCnt), "NPU HBM lifetime single-bit error count.", npuLabels),
		metric.NewCounterData("npu_hbm_total_double_bit_error_cnt", float64(eccInfo.TotalDoubleBitErrorCnt), "NPU HBM lifetime double-bit error count.", npuLabels),
		metric.NewCounterData("npu_hbm_single_bit_isolated_pages_cnt", float64(eccInfo.SingleBitIsolatedPagesCnt), "NPU HBM single-bit error isolated pages count.", npuLabels),
		metric.NewCounterData("npu_hbm_double_bit_isolated_pages_cnt", float64(eccInfo.DoubleBitIsolatedPagesCnt), "NPU HBM double-bit error isolated pages count.", npuLabels),
		)
	}

	return metrics, nil
}
