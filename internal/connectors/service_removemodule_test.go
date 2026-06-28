package connectors

import (
	"testing"

	"github.com/yogasw/wick/pkg/connector"
)

func TestRemoveModule(t *testing.T) {
	s := &Service{modules: map[string]connector.Module{}}
	s.modules["demo"] = connector.Module{Meta: connector.Meta{Key: "demo"}}
	if _, ok := s.Module("demo"); !ok {
		t.Fatal("precondition: demo should be present")
	}
	s.RemoveModule("demo")
	if _, ok := s.Module("demo"); ok {
		t.Fatal("RemoveModule should delete the module")
	}
}
