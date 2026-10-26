package fs

import (
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/genai-io/san/internal/task"
)

const (
	shellForeground        = "foreground"
	shellBackgroundJob     = "background_job"
	shellBackgroundService = "background_service"
	defaultShellTimeoutMS  = 120000
	maxShellTimeoutMS      = 600000
)

type shellExecutionOptions struct {
	background bool
	lifetime   task.Lifetime
	timeout    time.Duration
}

// parseShellExecutionOptions is shared by permission preparation and execution.
// Legacy arguments remain runtime-only compatibility inputs, not model options.
// Null means omitted, including when a strict provider fills optional fields.
func parseShellExecutionOptions(params map[string]any) (shellExecutionOptions, error) {
	background, hasBackground := false, params["run_in_background"] != nil
	if hasBackground {
		var ok bool
		background, ok = params["run_in_background"].(bool)
		if !ok {
			return shellExecutionOptions{}, fmt.Errorf("run_in_background must be a boolean or null")
		}
	}
	lifetime, hasLifetime := task.LifetimeJob, params["lifetime"] != nil
	if hasLifetime {
		value, ok := params["lifetime"].(string)
		if !ok || (value != string(task.LifetimeJob) && value != string(task.LifetimeService)) {
			return shellExecutionOptions{}, fmt.Errorf("lifetime must be 'job', 'service', or null")
		}
		lifetime = task.Lifetime(value)
	}
	mode := shellForeground
	if background {
		mode = shellBackgroundJob
		if lifetime == task.LifetimeService {
			mode = shellBackgroundService
		}
	}
	if raw := params["execution_mode"]; raw != nil {
		value, ok := raw.(string)
		if !ok || (value != shellForeground && value != shellBackgroundJob && value != shellBackgroundService) {
			return shellExecutionOptions{}, fmt.Errorf("execution_mode must be 'foreground', 'background_job', 'background_service', or null")
		}
		mode = value
	}
	options := shellExecutionOptions{background: mode != shellForeground, lifetime: task.LifetimeJob}
	if mode == shellBackgroundService {
		options.lifetime = task.LifetimeService
	}
	if hasBackground && background != options.background {
		return shellExecutionOptions{}, fmt.Errorf("execution_mode conflicts with run_in_background; use execution_mode alone")
	}
	if hasLifetime && lifetime != options.lifetime {
		return shellExecutionOptions{}, fmt.Errorf("lifetime conflicts with execution_mode; use execution_mode='background_service' for a service")
	}

	timeout, hasTimeout := defaultShellTimeoutMS*time.Millisecond, false
	for _, name := range []string{"timeout_ms", "timeout"} {
		if raw := params[name]; raw != nil {
			value, err := shellTimeoutValue(raw, name)
			if err != nil {
				return shellExecutionOptions{}, err
			}
			if hasTimeout && timeout != value {
				return shellExecutionOptions{}, fmt.Errorf("timeout_ms conflicts with timeout; use timeout_ms alone")
			}
			timeout, hasTimeout = value, true
		}
	}
	if options.lifetime == task.LifetimeService {
		if hasTimeout {
			return shellExecutionOptions{}, fmt.Errorf("background_service cannot specify an execution timeout; omit timeout_ms or use null")
		}
		timeout = 0
	}
	options.timeout = timeout
	return options, nil
}

func shellTimeoutValue(raw any, name string) (time.Duration, error) {
	var milliseconds float64
	switch value := raw.(type) {
	case int:
		milliseconds = float64(value)
	case int64:
		milliseconds = float64(value)
	case float64:
		milliseconds = value
	case json.Number:
		var err error
		milliseconds, err = value.Float64()
		if err != nil {
			return 0, fmt.Errorf("%s must be a positive integer in milliseconds", name)
		}
	default:
		return 0, fmt.Errorf("%s must be a positive integer in milliseconds", name)
	}
	if math.IsNaN(milliseconds) || math.IsInf(milliseconds, 0) || milliseconds < 1 || math.Trunc(milliseconds) != milliseconds {
		return 0, fmt.Errorf("%s must be a positive integer in milliseconds", name)
	}
	if name == "timeout" {
		// Keep the legacy cap, applied before converting to avoid overflow.
		milliseconds = min(milliseconds, maxShellTimeoutMS)
	} else if milliseconds > maxShellTimeoutMS {
		return 0, fmt.Errorf("timeout_ms must not exceed %d", maxShellTimeoutMS)
	}
	return time.Duration(milliseconds) * time.Millisecond, nil
}
