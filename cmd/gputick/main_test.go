package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func execute(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := newRootCmd()
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func TestCommand_Version_PrintsVersion(t *testing.T) {
	out, err := execute(t, "version")
	require.NoError(t, err)
	assert.Equal(t, "gputick dev\n", out)
}

func TestCommand_Stubs_NotImplemented(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "collect", args: []string{"collect"}},
		{name: "collect once", args: []string{"collect", "--once"}},
		{name: "serve", args: []string{"serve"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := execute(t, tt.args...)
			require.Error(t, err)
			assert.ErrorIs(t, err, errNotImplemented)
		})
	}
}

func TestCommand_Root_UnknownCommand(t *testing.T) {
	_, err := execute(t, "bogus")
	assert.Error(t, err)
}
