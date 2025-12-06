package controller

import (
	"context"
	"errors"
	"fmt"
	"os"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	notificationsv1alpha1 "github.com/AymenKoched/kubernetes-controller-interview/api/v1alpha1"
)

// DeploymentWatcherReconciler reconciles a DeploymentWatcher object
type DeploymentWatcherReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Cache  map[string]*appsv1.Deployment
}

func NewDeploymentWatcherReconciler(c client.Client, s *runtime.Scheme) *DeploymentWatcherReconciler {
	return &DeploymentWatcherReconciler{
		Client: c,
		Scheme: s,
		Cache:  map[string]*appsv1.Deployment{},
	}
}

func (r *DeploymentWatcherReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	key := req.Namespace + "/" + req.Name

	newD := &appsv1.Deployment{}
	err := r.Get(ctx, req.NamespacedName, newD)

	// Handle deletion
	if err != nil && client.IgnoreNotFound(err) == nil {
		if _, exists := r.Cache[key]; exists {
			text := buildDeletedMessage(req.Name, req.Namespace)
			delete(r.Cache, key)
			return r.createSlackMessage(ctx, req.Namespace, req.Name, text)
		}
		return ctrl.Result{}, nil
	}

	if err != nil {
		logger.Error(err, "failed to fetch deployment")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Handle creation
	if _, exists := r.Cache[key]; !exists {
		r.Cache[key] = newD.DeepCopy()
		text := buildCreatedMessage(newD)
		return r.createSlackMessage(ctx, newD.Namespace, newD.Name, text)
	}

	// Handle updates
	oldD := r.Cache[key]
	text := buildUpdatedMessage(oldD, newD)
	r.Cache[key] = newD.DeepCopy()
	return r.createSlackMessage(ctx, newD.Namespace, newD.Name, text)
}

func (r *DeploymentWatcherReconciler) createSlackMessage(
	ctx context.Context,
	namespace, deployName, text string,
) (ctrl.Result, error) {
	slackMsgNamespace := os.Getenv("SLACK_MESSAGES_NAMESPACE")
	if slackMsgNamespace == "" {
		return ctrl.Result{}, errors.New("SLACK_MESSAGES_NAMESPACE env variable not set")
	}

	// Check if namespace exists, if not create it
	ns := &corev1.Namespace{}
	err := r.Get(ctx, client.ObjectKey{Name: slackMsgNamespace}, ns)
	if err != nil && client.IgnoreNotFound(err) == nil {
		ns = &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: slackMsgNamespace,
			},
		}

		if createErr := r.Create(ctx, ns); createErr != nil {
			return ctrl.Result{}, fmt.Errorf("failed to create namespace %s: %w", slackMsgNamespace, createErr)
		}
	} else if err != nil {
		return ctrl.Result{}, err
	}

	sm := &notificationsv1alpha1.SlackMessage{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: fmt.Sprintf("slackmsg-%s-", deployName),
			Namespace:    slackMsgNamespace,
		},
		Spec: notificationsv1alpha1.SlackMessageSpec{
			Text: text,
			DeploymentRef: &notificationsv1alpha1.DeploymentRef{
				Namespace: namespace,
				Name:      deployName,
			},
		},
	}

	if err := r.Create(ctx, sm); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func buildCreatedMessage(d *appsv1.Deployment) string {
	return fmt.Sprintf(
		"🟢 Deployment *%s* created in namespace *%s*\nReplicas: %d",
		d.Name,
		d.Namespace,
		getReplicaCount(d),
	)
}

func buildDeletedMessage(name, namespace string) string {
	return fmt.Sprintf(
		"🚨 Deployment *%s* in namespace *%s* was *deleted*.",
		name,
		namespace,
	)
}

func buildUpdatedMessage(oldD, newD *appsv1.Deployment) string {
	oldReplicas := getReplicaCount(oldD)
	newReplicas := getReplicaCount(newD)
	if oldReplicas != newReplicas {
		return fmt.Sprintf(
			"📏 Scaled *%s* in namespace *%s*:\n%d → %d Replicas",
			newD.Name,
			newD.Namespace,
			oldReplicas,
			newReplicas,
		)
	}

	oldImg := oldD.Spec.Template.Spec.Containers[0].Image
	newImg := newD.Spec.Template.Spec.Containers[0].Image
	if oldImg != newImg {
		return fmt.Sprintf(
			"🖼️ Image updated for *%s* in namespace *%s*: %s → %s",
			newD.Name,
			newD.Namespace,
			oldImg,
			newImg,
		)
	}

	if !equality.Semantic.DeepEqual(oldD.Labels, newD.Labels) {
		return fmt.Sprintf(
			"🏷️ Labels updated for *%s* in namespace *%s*",
			newD.Name,
			newD.Namespace,
		)
	}

	if !equality.Semantic.DeepEqual(oldD.Annotations, newD.Annotations) {
		return fmt.Sprintf(
			"📝 Annotations updated for *%s* in namespace *%s*",
			newD.Name,
			newD.Namespace,
		)
	}

	return fmt.Sprintf(
		"📦 Deployment *%s* updated in namespace *%s*",
		newD.Name,
		newD.Namespace,
	)
}

func getReplicaCount(d *appsv1.Deployment) int32 {
	if d.Spec.Replicas == nil {
		return 0
	}
	return *d.Spec.Replicas
}

func predicateGenerationOrSpecChange() predicate.Predicate {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldObj := e.ObjectOld.(*appsv1.Deployment)
			newObj := e.ObjectNew.(*appsv1.Deployment)

			// Ignore pure status updates
			if equality.Semantic.DeepEqual(oldObj.Spec, newObj.Spec) {
				return false
			}

			return true
		},
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *DeploymentWatcherReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		// Uncomment the following line adding a pointer to an instance of the controlled resource as an argument
		For(&appsv1.Deployment{}).
		WithEventFilter(predicateGenerationOrSpecChange()).
		Named("deploymentwatcher").
		Complete(r)
}
