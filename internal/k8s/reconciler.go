package k8s

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	v1alpha1 "github.com/hauke-cloud/mqtt-bridge-controller/api/v1alpha1"
	"github.com/hauke-cloud/mqtt-bridge-controller/internal/bridge"
	mqttmgr "github.com/hauke-cloud/mqtt-bridge-controller/internal/mqtt"
)

const (
	finalizerName   = "iot.hauke.cloud/mqtt-bridge"
	requeueInterval = 15 * time.Second
)

// MQTTBridgeReconciler reconciles MQTTBridge resources.
type MQTTBridgeReconciler struct {
	client.Client
	Log      *slog.Logger
	Recorder record.EventRecorder
	Manager  *mqttmgr.Manager
}

func NewMQTTBridgeReconciler(
	c client.Client,
	log *slog.Logger,
	rec record.EventRecorder,
	mgr *mqttmgr.Manager,
) *MQTTBridgeReconciler {
	return &MQTTBridgeReconciler{
		Client:   c,
		Log:      log.With("controller", "mqttbridge"),
		Recorder: rec,
		Manager:  mgr,
	}
}

// SetupWithManager registers the reconciler with the controller-runtime manager.
func (r *MQTTBridgeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.MQTTBridge{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: 5}).
		Complete(r)
}

// +kubebuilder:rbac:groups=iot.hauke.cloud,resources=mqttbridges,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=iot.hauke.cloud,resources=mqttbridges/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=iot.hauke.cloud,resources=mqttbridges/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *MQTTBridgeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.With("bridge", req.NamespacedName)

	var br v1alpha1.MQTTBridge
	if err := r.Get(ctx, req.NamespacedName, &br); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !br.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, log, &br)
	}

	if !containsString(br.Finalizers, finalizerName) {
		br.Finalizers = append(br.Finalizers, finalizerName)
		if err := r.Update(ctx, &br); err != nil {
			return ctrl.Result{}, fmt.Errorf("add finalizer: %w", err)
		}
		return ctrl.Result{Requeue: true}, nil
	}

	return r.reconcileNormal(ctx, log, &br)
}

func (r *MQTTBridgeReconciler) reconcileNormal(
	ctx context.Context,
	log *slog.Logger,
	br *v1alpha1.MQTTBridge,
) (ctrl.Result, error) {
	spec, err := r.buildBridgeSpec(ctx, br)
	if err != nil {
		r.Recorder.Eventf(br, corev1.EventTypeWarning, "ConfigError", "%v", err)
		return r.updateStatus(ctx, br, v1alpha1.ConnectionStateError, err.Error())
	}

	if err := r.Manager.EnsureBridge(ctx, spec); err != nil {
		r.Recorder.Eventf(br, corev1.EventTypeWarning, "EnsureFailed", "%v", err)
		return r.updateStatus(ctx, br, v1alpha1.ConnectionStateError, err.Error())
	}

	// Sync real-time stats from MQTT manager into CR status.
	stats, err := r.Manager.Stats(br.Name, br.Namespace)
	if err != nil {
		log.WarnContext(ctx, "failed to get bridge stats", "error", err)
		return ctrl.Result{RequeueAfter: requeueInterval}, nil
	}

	if stats.State == bridge.StateConnected &&
		br.Status.ConnectionState != v1alpha1.ConnectionStateConnected {
		r.Recorder.Event(br, corev1.EventTypeNormal, "Connected", "MQTT bridge connected")
	}

	return r.syncStatus(ctx, br, stats)
}

func (r *MQTTBridgeReconciler) reconcileDelete(
	ctx context.Context,
	log *slog.Logger,
	br *v1alpha1.MQTTBridge,
) (ctrl.Result, error) {
	if err := r.Manager.RemoveBridge(ctx, br.Name, br.Namespace); err != nil {
		log.WarnContext(ctx, "failed to remove bridge on delete", "error", err)
	}

	br.Finalizers = removeString(br.Finalizers, finalizerName)
	if err := r.Update(ctx, br); err != nil {
		return ctrl.Result{}, fmt.Errorf("remove finalizer: %w", err)
	}
	return ctrl.Result{}, nil
}

func (r *MQTTBridgeReconciler) buildBridgeSpec(
	ctx context.Context,
	br *v1alpha1.MQTTBridge,
) (bridge.BridgeSpec, error) {
	port := br.Spec.Port
	if port == 0 {
		port = 1883
	}

	maxBackoff := time.Duration(br.Spec.MaxReconnectBackoffSeconds) * time.Second
	if maxBackoff == 0 {
		maxBackoff = 60 * time.Second
	}

	spec := bridge.BridgeSpec{
		Name:       br.Name,
		Namespace:  br.Namespace,
		Host:       br.Spec.Host,
		Port:       port,
		ClientID:   fmt.Sprintf("mqtt-bridge-ctrl-%s-%s", br.Namespace, br.Name),
		MaxBackoff: maxBackoff,
	}

	for _, t := range br.Spec.Topics {
		spec.Topics = append(spec.Topics, bridge.TopicSpec{
			Topic: t.Topic,
			QoS:   byte(t.QoS),
		})
	}

	if br.Spec.CredentialsSecretRef != nil {
		username, password, err := r.lookupCredentials(ctx, br.Spec.CredentialsSecretRef)
		if err != nil {
			return bridge.BridgeSpec{}, fmt.Errorf("%w: %w", bridge.ErrCredentialLookup, err)
		}
		spec.Username = username
		spec.Password = password
	}

	return spec, nil
}

func (r *MQTTBridgeReconciler) lookupCredentials(
	ctx context.Context,
	ref *v1alpha1.SecretKeyRef,
) (username, password string, err error) {
	var secret corev1.Secret
	key := types.NamespacedName{Name: ref.Name, Namespace: ref.Namespace}
	if err := r.Get(ctx, key, &secret); err != nil {
		return "", "", fmt.Errorf("get secret %s/%s: %w", ref.Namespace, ref.Name, err)
	}

	usernameKey := ref.UsernameKey
	if usernameKey == "" {
		usernameKey = "username"
	}
	passwordKey := ref.PasswordKey
	if passwordKey == "" {
		passwordKey = "password"
	}

	return string(secret.Data[usernameKey]), string(secret.Data[passwordKey]), nil
}

func (r *MQTTBridgeReconciler) syncStatus(
	ctx context.Context,
	br *v1alpha1.MQTTBridge,
	stats bridge.BridgeStats,
) (ctrl.Result, error) {
	patch := client.MergeFrom(br.DeepCopy())

	br.Status.ConnectionState = stats.State
	br.Status.MessagesReceived = stats.MessagesReceived
	br.Status.ReconnectCount = stats.ReconnectCount
	br.Status.ErrorMessage = stats.ErrorMessage

	if stats.LastConnectedTime != nil {
		t := metav1.NewTime(*stats.LastConnectedTime)
		br.Status.LastConnectedTime = &t
	}
	if stats.LastDisconnectedTime != nil {
		t := metav1.NewTime(*stats.LastDisconnectedTime)
		br.Status.LastDisconnectedTime = &t
	}

	setCondition(br, stats.State)

	if err := r.Status().Patch(ctx, br, patch); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("patch status: %w", err)
	}
	return ctrl.Result{RequeueAfter: requeueInterval}, nil
}

func (r *MQTTBridgeReconciler) updateStatus(
	ctx context.Context,
	br *v1alpha1.MQTTBridge,
	state v1alpha1.ConnectionState,
	msg string,
) (ctrl.Result, error) {
	patch := client.MergeFrom(br.DeepCopy())
	br.Status.ConnectionState = state
	br.Status.ErrorMessage = msg
	setCondition(br, state)

	if err := r.Status().Patch(ctx, br, patch); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("patch status: %w", err)
	}
	return ctrl.Result{RequeueAfter: requeueInterval}, nil
}

func setCondition(br *v1alpha1.MQTTBridge, state v1alpha1.ConnectionState) {
	status := metav1.ConditionFalse
	reason := "Disconnected"
	msg := "Bridge is not connected to the MQTT broker"

	switch state {
	case v1alpha1.ConnectionStateConnected:
		status = metav1.ConditionTrue
		reason = "Connected"
		msg = "Bridge is connected to the MQTT broker"
	case v1alpha1.ConnectionStateError:
		reason = "Error"
		msg = br.Status.ErrorMessage
	case v1alpha1.ConnectionStateConnecting:
		reason = "Connecting"
		msg = "Connecting to the MQTT broker"
	}

	now := metav1.Now()
	cond := metav1.Condition{
		Type:               v1alpha1.ConditionTypeConnected,
		Status:             status,
		Reason:             reason,
		Message:            msg,
		LastTransitionTime: now,
		ObservedGeneration: br.Generation,
	}

	for i, c := range br.Status.Conditions {
		if c.Type == cond.Type {
			if c.Status == cond.Status {
				// preserve stable transition time when status hasn't changed
				cond.LastTransitionTime = c.LastTransitionTime
			}
			br.Status.Conditions[i] = cond
			return
		}
	}
	br.Status.Conditions = append(br.Status.Conditions, cond)
}

func containsString(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

func removeString(slice []string, s string) []string {
	result := make([]string, 0, len(slice))
	for _, v := range slice {
		if v != s {
			result = append(result, v)
		}
	}
	return result
}
