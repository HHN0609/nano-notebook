package main

import (
	"reflect"
	"testing"
)

func TestLoadMigrationConfigUsesOnlyApplicationDatabase(t *testing.T) {
	t.Setenv("NANO_DATABASE_URL", "postgres://application")
	t.Setenv("NANO_COLLECTOR_DATABASE_URL", "postgres://observability")

	config := loadMigrationConfig()
	if config.ApplicationDatabaseURL != "postgres://application" {
		t.Fatalf("ApplicationDatabaseURL = %q", config.ApplicationDatabaseURL)
	}
	if _, found := reflect.TypeOf(config).FieldByName("CollectorDatabaseURL"); found {
		t.Fatal("migration config still exposes the retired Collector database")
	}
}
