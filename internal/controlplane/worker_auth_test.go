package controlplane

import (
	"context"
	"testing"

	"github.com/josephbolus/agentfactory/internal/protocol"
)

func TestEnrolledWorkerAuthenticatesBeforeFirstRegistration(t *testing.T) {
	store := newTestStore(t)
	enrollment, err := store.CreateWorkerEnrollment(context.Background(), "remote-worker")
	if err != nil {
		t.Fatal(err)
	}
	credential := "factory_worker_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := store.ExchangeWorkerEnrollment(context.Background(), enrollment.WorkerID, enrollment.EnrollmentToken, credential); err != nil {
		t.Fatal(err)
	}
	workerID, err := store.AuthenticateWorkerCredential(context.Background(), credential)
	if err != nil || workerID != enrollment.WorkerID {
		t.Fatalf("pre-registration authentication = %q, err %v", workerID, err)
	}
	worker, err := store.RegisterWorker(context.Background(), workerID, protocol.WorkerRegistration{
		Name: "Remote Worker", WorkerVersion: "test", ClaimProtocolVersion: protocol.ClaimProtocolVersion,
		Runtime: protocol.RuntimeCodex, RuntimeVersion: "codex-test", Capacity: 1, Health: "healthy",
	})
	if err != nil || worker.ID != enrollment.WorkerID {
		t.Fatalf("first authenticated registration = %#v, err %v", worker, err)
	}
}
