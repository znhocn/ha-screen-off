package ha

import (
	"strings"
	"testing"
	"time"
)

func TestNewValidates(t *testing.T) {
	for _, tc := range []struct {
		url, token, entity string
		ok                 bool
	}{
		{"http://ha:8123", "tok", "input_boolean.screen_power", true},
		{"http://ha:8123", "tok", "switch.monitor", true},
		{"http://ha:8123", "tok", "nodot", false},
		{"", "tok", "switch.monitor", false},
		{"http://ha:8123", "", "switch.monitor", false},
		{"http://ha:8123", "tok", "", false},
	} {
		_, err := New(tc.url, tc.token, tc.entity, time.Second, false)
		if (err == nil) != tc.ok {
			t.Errorf("New(%q,%q,%q) err=%v, want ok=%v", tc.url, tc.token, tc.entity, err, tc.ok)
		}
	}
}

func TestDomainDerivation(t *testing.T) {
	c, err := New("http://ha:8123", "tok", "switch.monitor", time.Second, false)
	if err != nil {
		t.Fatal(err)
	}
	if c.domain != "switch" {
		t.Fatalf("domain = %q", c.domain)
	}
	if c.entityID != "switch.monitor" {
		t.Fatalf("entityID = %q", c.entityID)
	}
	if !strings.HasPrefix(c.baseURL, "http://ha:8123") {
		t.Fatalf("baseURL = %q", c.baseURL)
	}
}

func TestBaseURLTrimmed(t *testing.T) {
	c, err := New("http://ha:8123/", "tok", "input_boolean.screen", time.Second, false)
	if err != nil {
		t.Fatal(err)
	}
	if c.baseURL != "http://ha:8123" {
		t.Fatalf("baseURL not trimmed: %q", c.baseURL)
	}
}

func TestStreamEntityState(t *testing.T) {
	if st, ok := streamEntityState(`{"entity_id":"input_boolean.screen_power","new_state":{"state":"off"}}`, "input_boolean.screen_power"); !ok || st != "off" {
		t.Fatalf("matching event: st=%q ok=%v", st, ok)
	}
	if _, ok := streamEntityState(`{"entity_id":"light.other","new_state":{"state":"on"}}`, "input_boolean.screen_power"); ok {
		t.Fatal("foreign entity must be ignored")
	}
	if _, ok := streamEntityState(`{"entity_id":"input_boolean.screen_power","new_state":null}`, "input_boolean.screen_power"); ok {
		t.Fatal("null new_state must be ignored")
	}
	if _, ok := streamEntityState(`not json`, "input_boolean.screen_power"); ok {
		t.Fatal("malformed payload must be ignored")
	}
}
