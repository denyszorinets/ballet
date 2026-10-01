package tenancy_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/denyszorinets/ballet/core/internal/domain/tenancy"
)

func TestValidateCustomerKey(t *testing.T) {
	tests := []struct {
		key string
		ok  bool
	}{
		{"acme", true},
		{"acme-corp", true},
		{"a1", true},
		{"a", false},
		{"Acme", false},
		{"1acme", false},
		{"acme_corp", false},
		{"-acme", false},
		{"acme-", false},
		{"abcdefghijklmnopqrstuvwxyzabcdefg", false}, // 33 chars
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			err := tenancy.ValidateCustomerKey(tt.key)
			if tt.ok {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

func TestValidateProjectKey(t *testing.T) {
	tests := []struct {
		key string
		ok  bool
	}{
		{"ACME", true},
		{"AB", true},
		{"WEB2", true},
		{"A", false},
		{"acme", false},
		{"2WEB", false},
		{"AC-ME", false},
		{"ABCDEFGHIJK", false}, // 11 chars
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			err := tenancy.ValidateProjectKey(tt.key)
			if tt.ok {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

func TestValidateName(t *testing.T) {
	assert.NoError(t, tenancy.ValidateName("Acme Corporation"))
	assert.Error(t, tenancy.ValidateName(""))
	assert.Error(t, tenancy.ValidateName("   "))
	assert.Error(t, tenancy.ValidateName(string(make([]byte, 201))))
}
