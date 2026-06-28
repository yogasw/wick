package connector

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/yogasw/wick/pkg/entity"
	"github.com/yogasw/wick/pkg/wickdocs"
)

func TestModuleJSONRoundTrip(t *testing.T) {
	exec := func(c *Ctx) (any, error) { return nil, nil }
	orig := Module{
		Meta: Meta{Key: "gws", Name: "Google Workspace", Description: "d", Icon: "i"},
		Configs: entity.StructToConfigs(struct {
			Token string `wick:"token,secret"`
		}{}),
		Operations: []Category{
			Cat("Drive", "files",
				Op("list", "List", "list files", struct {
					Folder string `wick:"folder"`
				}{}, exec, wickdocs.Docs{}),
				OpDestructive("rm", "Remove", "delete file", struct {
					ID string `wick:"id"`
				}{}, exec, wickdocs.Docs{}),
			),
		},
		HealthCheck:        func(c *Ctx) ([]OpHealth, error) { return nil, nil },
		AllowSessionConfig: true,
		DefaultAccess:      AccessDefaults{EnableSSO: true, AllowOthersConnectSSO: true},
		OAuth: &OAuthMeta{
			AuthorizeURL: "https://accounts.google.com/o/oauth2/auth",
			TokenURL:     "https://oauth2.googleapis.com/token",
			Scopes:       "drive.readonly",
			DisplayName:  "Google",
			GetUserIdentity: func(ctx context.Context, accessToken string) (userID, displayName string, err error) {
				return "", "", nil
			},
		},
	}

	b, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal must succeed now: %v", err)
	}

	var got Module
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Meta.Key != "gws" || got.Meta.Name != "Google Workspace" {
		t.Fatalf("meta lost: %+v", got.Meta)
	}
	if len(got.Configs) != len(orig.Configs) {
		t.Fatalf("configs lost: %d vs %d", len(got.Configs), len(orig.Configs))
	}
	ops := got.AllOps()
	if len(ops) != 2 || ops[0].Key != "list" || ops[1].Key != "rm" {
		t.Fatalf("ops lost: %+v", ops)
	}
	if !ops[1].Destructive {
		t.Fatal("destructive flag lost")
	}
	if len(ops[0].Input) != 1 || ops[0].Input[0].Key != "folder" {
		t.Fatalf("op input lost: %+v", ops[0].Input)
	}
	if !got.AllowSessionConfig {
		t.Fatal("AllowSessionConfig lost")
	}
	if !got.DefaultAccess.EnableSSO || !got.DefaultAccess.AllowOthersConnectSSO {
		t.Fatalf("DefaultAccess lost: %+v", got.DefaultAccess)
	}
	if got.OAuth == nil || got.OAuth.AuthorizeURL == "" || got.OAuth.Scopes != "drive.readonly" {
		t.Fatalf("OAuth lost: %+v", got.OAuth)
	}
	if ops[0].Execute != nil || got.HealthCheck != nil || got.OAuth.GetUserIdentity != nil {
		t.Fatal("func fields should be nil after unmarshal")
	}
}
