package ddevapp

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	composeTypes "github.com/compose-spec/compose-go/v2/types"
	"github.com/stretchr/testify/require"
)

func TestDockerfileBaseImages(t *testing.T) {
	override := "ddev/ddev-webserver:override"
	tests := []struct {
		name       string
		dockerfile string
		args       composeTypes.MappingWithEquals
		want       []string
		wantErr    bool
	}{
		{
			name:       "ARG default",
			dockerfile: "ARG BASE_IMAGE=\"ddev/ddev-webserver:v1\"\nFROM $BASE_IMAGE\nRUN true\n",
			want:       []string{"ddev/ddev-webserver:v1"},
		},
		{
			name:       "build arg wins over the default",
			dockerfile: "ARG BASE_IMAGE=\"scratch\"\nFROM ${BASE_IMAGE}\n",
			args:       composeTypes.MappingWithEquals{"BASE_IMAGE": &override},
			want:       []string{override},
		},
		{
			name:       "stages and scratch are not base images",
			dockerfile: "FROM --platform=linux/amd64 node:20 AS builder\nFROM scratch\nFROM builder\nCOPY --from=builder /x /x\n",
			want:       []string{"node:20"},
		},
		{
			name:       "unresolved variable",
			dockerfile: "FROM $UNKNOWN\n",
			wantErr:    true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "Dockerfile")
			require.NoError(t, os.WriteFile(path, []byte(tc.dockerfile), 0644))
			got, err := dockerfileBaseImages(path, tc.args)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestHashBuildContext(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0644))

	hashOf := func() string {
		h := sha256.New()
		require.True(t, hashBuildContext(h, dir))
		return fmt.Sprintf("%x", h.Sum(nil))
	}
	first := hashOf()
	require.Equal(t, first, hashOf())

	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\nRUN true\n"), 0644))
	require.NotEqual(t, first, hashOf())

	for i := 0; i <= buildFingerprintMaxFiles; i++ {
		require.NoError(t, os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%d", i)), nil, 0644))
	}
	require.False(t, hashBuildContext(sha256.New(), dir), "a context over the file limit is built rather than hashed")
}
