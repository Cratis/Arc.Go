package metadata_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/cratis/arc.go/metadata"
)

func TestAuthorizationDescriptorRoundTripAndRoutes(t *testing.T) {
	catalog := metadata.Catalog{Version: 1, Commands: []metadata.Command{{Type: metadata.TypeName{Name: "Create"}}}, Queries: []metadata.Query{{ReadModel: metadata.TypeName{Name: "Item"}, Name: "All"}}}
	original, err := metadata.Resolve(catalog, metadata.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	catalog.Commands[0].Authorization = &metadata.Authorization{}
	catalog.Queries[0].Authorization = &metadata.Authorization{AllowAnonymous: true}
	catalog.Queries[0].ReadModelAuthorization = &metadata.Authorization{Requirements: []metadata.AuthorizationRequirement{{Roles: []string{"Reader", "Owner"}, Policy: "tenant", AuthenticationSchemes: []string{"external"}}}}
	data, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	var decoded metadata.Catalog
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(catalog, decoded) {
		t.Fatalf("round trip = %#v", decoded)
	}
	routes, err := metadata.Resolve(decoded, metadata.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, routes) {
		t.Fatal("authorization changed routes")
	}
	if decoded.Commands[0].Authorization == nil {
		t.Fatal("explicit empty lost")
	}
}
