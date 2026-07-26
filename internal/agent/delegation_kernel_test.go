package agent

import "testing"

func TestDelegationLifecycleHasThreeStableTerminalStatesAndSeparateConsumption(t *testing.T) {
	for _, terminal := range []DelegationState{DelegationSucceeded, DelegationFailed, DelegationCancelled} {
		delegation := DelegationLifecycle{State: DelegationWaiting}
		errorCode := "safe_code"
		if terminal == DelegationSucceeded {
			errorCode = ""
		}
		if err := delegation.Terminalize(terminal, errorCode); err != nil {
			t.Fatalf("terminal=%q err=%v", terminal, err)
		}
		if err := delegation.Consume(); err != nil || delegation.State != terminal || !delegation.Consumed {
			t.Fatalf("delegation=%+v err=%v", delegation, err)
		}
		if err := delegation.Terminalize(terminal, errorCode); err == nil {
			t.Fatalf("terminal state %q transitioned twice", terminal)
		}
	}
}

func TestDelegationLifecycleRejectsConsumptionWhileWaitingAndInvalidTerminalShape(t *testing.T) {
	waiting := DelegationLifecycle{State: DelegationWaiting}
	if err := waiting.Consume(); err == nil {
		t.Fatal("consumed waiting delegation")
	}
	if err := waiting.Terminalize(DelegationWaiting, ""); err == nil {
		t.Fatal("accepted waiting as terminal")
	}
	success := DelegationLifecycle{State: DelegationWaiting}
	if err := success.Terminalize(DelegationSucceeded, "unexpected_error"); err == nil {
		t.Fatal("accepted error code on success")
	}
	failure := DelegationLifecycle{State: DelegationWaiting}
	if err := failure.Terminalize(DelegationFailed, ""); err == nil {
		t.Fatal("accepted missing failure code")
	}
}
