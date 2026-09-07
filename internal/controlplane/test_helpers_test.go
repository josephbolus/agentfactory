package controlplane

import (
	"context"
	"testing"

	"github.com/josephbolus/agentfactory/internal/protocol"
)

const (
	workerA = "worker-a"
	tokenA  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

func boolPointer(value bool) *bool {
	return &value
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(context.Background(), t.TempDir()+"/controlplane.sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	store.githubIssues = &fakeWorkflowSource{
		commit: "test-commit",
		files: map[string][]byte{
			".factory/workflows/implement.md": []byte(protocol.StandardBuildProcedurePrompt),
		},
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	return store
}

func registerTestWorker(
	t *testing.T,
	store *Store,
	id string,
	capacity int,
	repositories ...protocol.RepositoryRegistration,
) protocol.Worker {
	t.Helper()
	worker, err := store.RegisterWorker(context.Background(), id, protocol.WorkerRegistration{
		Name: id, WorkerVersion: "test", ClaimProtocolVersion: protocol.ClaimProtocolVersion,
		Runtime:        protocol.RuntimeCodex,
		RuntimeVersion: "codex-test", Capacity: capacity, Health: "healthy",
		Repositories: repositories,
	})
	if err != nil {
		t.Fatal(err)
	}
	return worker
}
