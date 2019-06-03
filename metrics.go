package main

import (
	"sync"
)

var metrics map[string]int64
var metricsMutex = sync.RWMutex{}

func addMetric(obj string) {
	metricsMutex.Lock()
	metrics[obj] = ktime()
	metricsMutex.Unlock()
}
func getMetric(obj string) int {
	metricsMutex.RLock()
	v, ok := metrics[obj]
	metricsMutex.RUnlock()
	if ok {
		return int(ktime() - v)
	}
	return -1
}