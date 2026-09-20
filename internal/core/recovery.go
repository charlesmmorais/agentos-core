package core

import (
	"errors"
	"os"
	"time"
)

type RecoveryState struct {
	BackupSHA256   string    `json:"backup_sha256"`
	OriginalStatus string    `json:"original_status"`
	RestoredAt     time.Time `json:"restored_at"`
	Ready          bool      `json:"ready"`
}

func RecoveryReady(st *State) error {
	if st.Recovery != nil && !st.Recovery.Ready {
		return errors.New("restored mission requires recover --source-fenced before activation")
	}
	return nil
}

// Doctor verifies local dependencies; it never queries models or write targets.
func Doctor(s *Store, st *State) error {
	if err := st.validateActions(); err != nil {
		return err
	}
	if st.Execution == nil {
		return errors.New("execution manifest missing")
	}
	var actual *Manifest
	var err error
	switch st.Execution.Runtime {
	case "":
		actual, err = Inspect(st.Protocol)
	case "docker":
		actual, err = InspectDocker(st.Protocol, st.Execution.Image)
	default:
		return errors.New("unsupported runtime")
	}
	if err != nil {
		return err
	}
	if *actual != *st.Execution {
		return errors.New("script or runtime does not match the preserved execution manifest")
	}
	for _, a := range st.Artifacts {
		if _, err := s.ReadArtifact(a); err != nil {
			return err
		}
	}
	stage, err := os.MkdirTemp("", "agentos-doctor-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	return snapshotWorkspace(st.Protocol.Workspace, stage)
}
func ActivateRecovery(s *Store, st *State, sourceFenced bool) error {
	if st.Recovery == nil {
		return errors.New("mission is not a restored backup")
	}
	if !sourceFenced {
		return errors.New("confirm the original instance is stopped/fenced with --source-fenced")
	}
	if err := Doctor(s, st); err != nil {
		return err
	}
	st.Recovery.Ready = true
	st.Events = append(st.Events, Event{time.Now().UTC(), "recovery_validated_source_fenced", st.Completed})
	return s.Save(st)
}
