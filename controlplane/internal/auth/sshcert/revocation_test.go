package sshcert_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stripe/krl"

	"github.com/yanet-platform/yanet2/controlplane/internal/auth/sshcert"
)

func TestRevocationChecker_Revoked(t *testing.T) {
	ca := generateTestCA(t)
	cert, _ := generateTestUserCert(
		t,
		ca,
		"alice",
		42,
		time.Now().Add(-1*time.Hour),
		time.Now().Add(24*time.Hour),
	)

	krlData := buildTestKRL(t, ca, []uint64{42})
	k, err := krl.ParseKRL(krlData)
	require.NoError(t, err)

	checker := sshcert.NewKRLRevocationChecker(k)
	err = checker.IsRevoked(cert)
	require.ErrorIs(t, err, sshcert.ErrCertRevoked)
}

func TestRevocationChecker_NotRevoked(t *testing.T) {
	ca := generateTestCA(t)
	cert, _ := generateTestUserCert(
		t,
		ca,
		"alice",
		42,
		time.Now().Add(-1*time.Hour),
		time.Now().Add(24*time.Hour),
	)

	// Revoke serial 99, not 42.
	krlData := buildTestKRL(t, ca, []uint64{99})
	k, err := krl.ParseKRL(krlData)
	require.NoError(t, err)

	checker := sshcert.NewKRLRevocationChecker(k)
	err = checker.IsRevoked(cert)
	require.NoError(t, err)
}
