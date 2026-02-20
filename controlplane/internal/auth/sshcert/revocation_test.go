package sshcert

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stripe/krl"
)

func TestRevocationChecker_Revoked(t *testing.T) {
	ca := generateCA(t)
	cert, _ := generateUserCert(
		t,
		ca,
		"alice",
		42,
		time.Now().Add(-1*time.Hour),
		time.Now().Add(24*time.Hour),
	)

	krlData := buildKRL(t, ca, []uint64{42})
	k, err := krl.ParseKRL(krlData)
	require.NoError(t, err)

	checker := NewKRLRevocationChecker(k)
	err = checker.IsRevoked(cert)
	require.ErrorIs(t, err, ErrCertRevoked)
}

func TestRevocationChecker_NotRevoked(t *testing.T) {
	ca := generateCA(t)
	cert, _ := generateUserCert(
		t,
		ca,
		"alice",
		42,
		time.Now().Add(-1*time.Hour),
		time.Now().Add(24*time.Hour),
	)

	// Revoke serial 99, not 42.
	krlData := buildKRL(t, ca, []uint64{99})
	k, err := krl.ParseKRL(krlData)
	require.NoError(t, err)

	checker := NewKRLRevocationChecker(k)
	err = checker.IsRevoked(cert)
	require.NoError(t, err)
}
