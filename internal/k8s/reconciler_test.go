package k8s

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1alpha1 "github.com/hauke-cloud/mqtt-bridge-controller/api/v1alpha1"
)

func TestSetCondition_Connected(t *testing.T) {
	br := &v1alpha1.MQTTBridge{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}

	setCondition(br, v1alpha1.ConnectionStateConnected)

	if len(br.Status.Conditions) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(br.Status.Conditions))
	}
	c := br.Status.Conditions[0]
	if c.Type != v1alpha1.ConditionTypeConnected {
		t.Errorf("type: got %s, want %s", c.Type, v1alpha1.ConditionTypeConnected)
	}
	if c.Status != metav1.ConditionTrue {
		t.Errorf("status: got %s, want True", c.Status)
	}
	if c.Reason != "Connected" {
		t.Errorf("reason: got %s, want Connected", c.Reason)
	}
}

func TestSetCondition_Error(t *testing.T) {
	br := &v1alpha1.MQTTBridge{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Status: v1alpha1.MQTTBridgeStatus{
			ErrorMessage: "connection refused",
		},
	}

	setCondition(br, v1alpha1.ConnectionStateError)

	c := br.Status.Conditions[0]
	if c.Status != metav1.ConditionFalse {
		t.Errorf("status: got %s, want False", c.Status)
	}
	if c.Reason != "Error" {
		t.Errorf("reason: got %s, want Error", c.Reason)
	}
}

func TestSetCondition_StableTransitionTime(t *testing.T) {
	br := &v1alpha1.MQTTBridge{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}

	// First call sets transition time.
	setCondition(br, v1alpha1.ConnectionStateConnected)
	first := br.Status.Conditions[0].LastTransitionTime

	// Second call with same status must preserve the transition time.
	setCondition(br, v1alpha1.ConnectionStateConnected)
	second := br.Status.Conditions[0].LastTransitionTime

	if !first.Equal(&second) {
		t.Error("transition time changed on identical status update")
	}
}

func TestSetCondition_TransitionTimeChanges(t *testing.T) {
	br := &v1alpha1.MQTTBridge{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}

	setCondition(br, v1alpha1.ConnectionStateConnected)
	setCondition(br, v1alpha1.ConnectionStateDisconnected)

	if br.Status.Conditions[0].Status != metav1.ConditionFalse {
		t.Error("expected False after disconnect")
	}
}

func TestContainsString(t *testing.T) {
	cases := []struct {
		slice []string
		s     string
		want  bool
	}{
		{[]string{"a", "b", "c"}, "b", true},
		{[]string{"a", "b", "c"}, "d", false},
		{nil, "a", false},
	}
	for _, tc := range cases {
		if got := containsString(tc.slice, tc.s); got != tc.want {
			t.Errorf("containsString(%v, %q) = %v, want %v", tc.slice, tc.s, got, tc.want)
		}
	}
}

func TestRemoveString(t *testing.T) {
	cases := []struct {
		slice []string
		s     string
		want  []string
	}{
		{[]string{"a", "b", "c"}, "b", []string{"a", "c"}},
		{[]string{"a", "b"}, "x", []string{"a", "b"}},
		{nil, "a", []string{}},
	}
	for _, tc := range cases {
		got := removeString(tc.slice, tc.s)
		if len(got) != len(tc.want) {
			t.Errorf("removeString(%v, %q) = %v, want %v", tc.slice, tc.s, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("removeString[%d]: got %s, want %s", i, got[i], tc.want[i])
			}
		}
	}
}
