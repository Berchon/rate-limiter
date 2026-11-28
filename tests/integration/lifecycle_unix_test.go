//go:build unix

package integration

import (
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/Berchon/rate-limiter/internal/infra/app"
	"github.com/Berchon/rate-limiter/internal/infra/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplicationStartHandlesSIGTERM(t *testing.T) {
	port := freePort(t)
	loadConfigWithOptions(t, "", defaultEnvOptions(config.StorageTypeMemory, config.AlgorithmSlidingWindow))
	t.Setenv("PORT", port)

	// Safety net: guarantees the default SIGTERM handler can never kill the test binary,
	// even if Start has not registered its own handler yet. Must never run in parallel.
	guard := make(chan os.Signal, 1)
	signal.Notify(guard, syscall.SIGTERM)
	defer signal.Stop(guard)

	application, err := app.NewAppWithLogger(discardLogger())
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() { done <- application.Start() }()

	waitForServer(t, "http://127.0.0.1:"+port+"/health")

	require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGTERM))
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("application did not stop after SIGTERM")
	}
}
