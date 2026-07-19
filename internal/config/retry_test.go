package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestDefaultRetrySettingsFreezeCurrentRuntimeBehavior(t *testing.T) {
	got := Default().Retry
	want := RetryConfig{ReconnectDelaysMS: []int64{100, 250, 500}, JobRetryDelayMS: 60_000}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("retry defaults = %#v, want %#v", got, want)
	}
}

func TestDecodeAppliesPartialRetrySettings(t *testing.T) {
	got, err := Decode(strings.NewReader(`{"schema_version":1,"retry":{"job_retry_delay_ms":120000}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Retry.ReconnectDelaysMS, []int64{100, 250, 500}) || got.Retry.JobRetryDelayMS != 120_000 {
		t.Fatalf("partial retry = %#v", got.Retry)
	}

	disabled, err := Decode(strings.NewReader(`{"schema_version":1,"retry":{"reconnect_delays_ms":[]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(disabled.Retry.ReconnectDelaysMS) != 0 {
		t.Fatalf("explicitly disabled reconnect delays = %v", disabled.Retry.ReconnectDelaysMS)
	}
}

func TestRetrySettingsRemainBoundedAndNonAggressive(t *testing.T) {
	tests := []struct {
		name, input, want string
	}{
		{name: "too many reconnects", input: `{"schema_version":1,"retry":{"reconnect_delays_ms":[100,250,500,1000]}}`, want: "retry.reconnect_delays_ms"},
		{name: "null reconnect schedule", input: `{"schema_version":1,"retry":{"reconnect_delays_ms":null}}`, want: "retry.reconnect_delays_ms"},
		{name: "faster first reconnect", input: `{"schema_version":1,"retry":{"reconnect_delays_ms":[99]}}`, want: "retry.reconnect_delays_ms[0]"},
		{name: "faster later reconnect", input: `{"schema_version":1,"retry":{"reconnect_delays_ms":[100,249]}}`, want: "retry.reconnect_delays_ms[1]"},
		{name: "zero reconnect delay", input: `{"schema_version":1,"retry":{"reconnect_delays_ms":[0]}}`, want: "retry.reconnect_delays_ms[0]"},
		{name: "negative reconnect delay", input: `{"schema_version":1,"retry":{"reconnect_delays_ms":[-1]}}`, want: "retry.reconnect_delays_ms[0]"},
		{name: "decreasing reconnect delays", input: `{"schema_version":1,"retry":{"reconnect_delays_ms":[300,250]}}`, want: "retry.reconnect_delays_ms[1]"},
		{name: "excessive reconnect delay", input: `{"schema_version":1,"retry":{"reconnect_delays_ms":[30001]}}`, want: "retry.reconnect_delays_ms[0]"},
		{name: "faster Job retry", input: `{"schema_version":1,"retry":{"job_retry_delay_ms":59999}}`, want: "retry.job_retry_delay_ms"},
		{name: "excessive Job retry", input: `{"schema_version":1,"retry":{"job_retry_delay_ms":600001}}`, want: "retry.job_retry_delay_ms"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertDecodeErrorContains(t, test.input, test.want)
		})
	}
}
