package webui

import (
	"reflect"
	"testing"
)

func TestWorkerArgs(t *testing.T) {
	got, err := workerArgs("scrape", "12345")
	want := []string{"--realtime", "--config-json-stdin", "--scrape-class", "12345"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("workerArgs = %v, %v; want %v", got, err, want)
	}
	for _, id := range []string{"0", "12x", "9223372036854775808"} {
		if _, err := workerArgs("scrape", id); err == nil {
			t.Errorf("workerArgs accepted invalid class ID %q", id)
		}
	}
}
