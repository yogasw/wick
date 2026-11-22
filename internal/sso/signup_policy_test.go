package sso

import (
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

func svcWith(p entity.SSOProvider) *Service {
	return &Service{cache: map[string]entity.SSOProvider{p.Provider: p}}
}

func TestSignupPolicy(t *testing.T) {
	cases := []struct {
		name string
		row  entity.SSOProvider
		mail string
		want SignupPolicy
	}{
		{"default keeps old behaviour", entity.SSOProvider{}, "dev@acme.test", SignupPolicy{AllowCreate: true}},
		{"listed domain auto-approves", entity.SSOProvider{AutoApproveDomains: "acme.test,example.com"}, "dev@ACME.test", SignupPolicy{AllowCreate: true, AutoApprove: true}},
		{"other domain stays pending", entity.SSOProvider{AutoApproveDomains: "acme.test"}, "dev@other.test", SignupPolicy{AllowCreate: true}},
		{"subdomain is not the domain", entity.SSOProvider{AutoApproveDomains: "acme.test"}, "dev@mail.acme.test", SignupPolicy{AllowCreate: true}},
		{"registration closed", entity.SSOProvider{DisableAutoRegister: true}, "dev@acme.test", SignupPolicy{}},
		{"closed but domain approves existing", entity.SSOProvider{DisableAutoRegister: true, AutoApproveDomains: "acme.test"}, "dev@acme.test", SignupPolicy{AutoApprove: true}},
		{"malformed email", entity.SSOProvider{AutoApproveDomains: "acme.test"}, "acme.test", SignupPolicy{AllowCreate: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.row.Provider = entity.SSOProviderGoogle
			got := svcWith(tc.row).SignupPolicy(entity.SSOProviderGoogle, tc.mail)
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestSignupPolicyUnknownProvider(t *testing.T) {
	s := &Service{cache: map[string]entity.SSOProvider{}}
	if got := s.SignupPolicy("nope", "dev@acme.test"); got != (SignupPolicy{}) {
		t.Fatalf("unknown provider = %+v, want zero", got)
	}
}

func TestIsEmailAllowedUnchanged(t *testing.T) {
	s := svcWith(entity.SSOProvider{Provider: entity.SSOProviderGoogle, AllowedDomains: "acme.test"})
	if !s.IsEmailAllowed(entity.SSOProviderGoogle, "dev@Acme.test") {
		t.Fatal("listed domain refused")
	}
	if s.IsEmailAllowed(entity.SSOProviderGoogle, "dev@other.test") {
		t.Fatal("unlisted domain allowed")
	}
	open := svcWith(entity.SSOProvider{Provider: entity.SSOProviderGoogle})
	if !open.IsEmailAllowed(entity.SSOProviderGoogle, "dev@other.test") {
		t.Fatal("empty list must allow every domain")
	}
}
