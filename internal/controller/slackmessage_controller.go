package controller

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/slack-go/slack"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	notificationsv1alpha1 "github.com/AymenKoched/kubernetes-controller-interview/api/v1alpha1"
)

// SlackMessageReconciler reconciles a SlackMessage object
type SlackMessageReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *SlackMessageReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the SlackMessage instance
	sm := &notificationsv1alpha1.SlackMessage{}
	if err := r.Get(ctx, req.NamespacedName, sm); err != nil {
		logger.Error(err, "failed to fetch SlackMessage", "name", req.NamespacedName)
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// If the message has already been sent, skip
	if sm.Status.Sent {
		logger.Info("Message already sent, skipping")
		return ctrl.Result{}, nil
	}

	// Send the message to Slack
	if err := r.sendToSlack(ctx, sm); err != nil {
		logger.Error(err, "Failed to send SlackMessage")

		sm.Status.Sent = false
		sm.Status.Error = err.Error()
		sm.Status.SentAt = nil
		if err := r.Status().Update(ctx, sm); err != nil {
			logger.Error(err, "Failed to update SlackMessage status")
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, err
	}

	// Update the status of the SlackMessage
	now := metav1.NewTime(time.Now())
	sm.Status.Sent = true
	sm.Status.Error = ""
	sm.Status.SentAt = &now
	if err := r.Status().Update(ctx, sm); err != nil {
		logger.Error(err, "Failed to update SlackMessage status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *SlackMessageReconciler) sendToSlack(ctx context.Context, slackMessage *notificationsv1alpha1.SlackMessage) error {
	logger := log.FromContext(ctx)

	token := os.Getenv("SLACK_TOKEN")
	if token == "" {
		return errors.New("SLACK_TOKEN env variable not set")
	}

	channel := os.Getenv("SLACK_CHANNEL")
	if channel == "" {
		return errors.New("SLACK_CHANNEL env variable not set")
	}

	slackClient := slack.New(token)

	_, _, err := slackClient.PostMessage(
		channel,
		slack.MsgOptionText(slackMessage.Spec.Text, false),
	)

	if err != nil {
		logger.Error(err, "Slack API error")
		return err
	}

	logger.Info("Slack message sent")
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SlackMessageReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&notificationsv1alpha1.SlackMessage{}).
		Named("slackmessage").
		Complete(r)
}
