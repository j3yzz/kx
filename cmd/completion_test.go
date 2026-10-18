package cmd

import (
	"reflect"
	"testing"

	"github.com/j3yzz/kx/internal/config"
)

func TestMergeAliases(t *testing.T) {
	got := mergeAliases([]string{"payments", "mysql"}, map[string]config.Alias{"pay": {App: "payments"}, "mysql": {App: "mysql"}})
	if want := []string{"mysql", "pay", "payments"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}
