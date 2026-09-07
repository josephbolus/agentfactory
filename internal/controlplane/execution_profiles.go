package controlplane

import (
	"context"
	"database/sql"

	"github.com/josephbolus/agentfactory/internal/protocol"
)

func persistentAutoProfile() protocol.ExecutionProfile {
	return protocol.ExecutionProfile{
		ID: protocol.PersistentAutoProfileID, Name: "Persistent auto", Kind: protocol.BackendPersistent,
		Version: 1, Provider: "worker", Model: "worker-default", ResourceClass: "worker",
		MaxConcurrent: protocol.MaxWorkerCapacity, Enabled: true, Healthy: true,
	}
}

func (s *Store) ExecutionProfiles(ctx context.Context) (protocol.ExecutionProfilePage, error) {
	return protocol.ExecutionProfilePage{Profiles: []protocol.ExecutionProfile{persistentAutoProfile()}}, nil
}

func (s *Store) ExecutionProfile(ctx context.Context, id string) (protocol.ExecutionProfile, error) {
	if id == "" || id == protocol.PersistentAutoProfileID {
		return persistentAutoProfile(), nil
	}
	return protocol.ExecutionProfile{}, ErrNotFound
}

// loadExecutionSnapshot resolves the execution plan for an admitted Task.
// Local persistent Workers are the only execution backend.
func loadExecutionSnapshot(
	ctx context.Context,
	tx *sql.Tx,
	task protocol.TaskSnapshot,
	requestedProfileID string,
) (protocol.ExecutionSnapshot, bool, string, error) {
	profileID := requestedProfileID
	if profileID == "" {
		profileID = task.ExecutionProfileID
	}
	if profileID != "" && profileID != protocol.PersistentAutoProfileID {
		return protocol.ExecutionSnapshot{}, false, "", invalid("execution_profile_not_found", "the selected execution profile does not exist")
	}
	return protocol.ExecutionSnapshot{
		ProfileID: protocol.PersistentAutoProfileID, ProfileVersion: 1,
		Backend: protocol.BackendPersistent, Runtime: task.Runtime,
		Provider: "worker", Model: "worker-default", TimeoutSeconds: task.TimeoutSeconds,
		ResourceClass: "worker", CommitResolutionPolicy: protocol.CommitResolvePerAttempt,
	}, true, "", nil
}

func validateTaskExecutionProfile(ctx context.Context, tx *sql.Tx, id string) error {
	if id == "" || id == protocol.PersistentAutoProfileID {
		return nil
	}
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM execution_profiles WHERE id = ?`, id).Scan(&exists); err != nil {
		return unavailable(err)
	}
	if exists == 0 {
		return invalid("execution_profile_not_found", "the selected execution profile does not exist")
	}
	return nil
}

func validateOutcomeContractBackend(
	ctx context.Context,
	tx *sql.Tx,
	contract protocol.OutcomeContract,
	profileID string,
) error {
	if contract != protocol.OutcomeAgentUpdate || profileID == "" || profileID == protocol.PersistentAutoProfileID {
		return nil
	}
	return conflict("agent_update_backend_unsupported", "agent_update Work requires a persistent Worker backend")
}
