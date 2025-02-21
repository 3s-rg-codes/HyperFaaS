package scheduling

import (
	"context"
	"io"
	"log/slog"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/state"
	controllerpb "github.com/3s-rg-codes/HyperFaaS/proto/controller"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	// If multiple leaf nodes listen to the same worker, each needs a different leafNodeID
	leafNodeID = "todo"
)

// The reconciler is responsible for reconciling the state of the workers and instances.
// It reads the StatusUpdate stream from the workers and updates the state of the scheduler if necessary.
// The most common case is that instances time out when waiting for more calls.
type Reconciler struct {
	workerIDs []state.WorkerID
	workers   *state.Workers
	logger    *slog.Logger
}

func NewReconciler(workerIDs []state.WorkerID, workers *state.Workers, logger *slog.Logger) *Reconciler {
	return &Reconciler{
		workerIDs: workerIDs,
		workers:   workers,
		logger:    logger,
	}
}

func (r *Reconciler) Run(ctx context.Context) {
	for _, workerID := range r.workerIDs {
		go r.ListenToWorkerStatusUpdates(ctx, workerID)
	}
}

func (r *Reconciler) getStatusUpdateStream(ctx context.Context, workerID state.WorkerID) (controllerpb.Controller_StatusClient, *grpc.ClientConn, error) {

	conn, err := grpc.NewClient(string(workerID), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		r.logger.Error("Failed to create gRPC client", "error", err)
		return nil, nil, err
	}

	client := controllerpb.NewControllerClient(conn)

	statusUpdates, err := client.Status(ctx, &controllerpb.StatusRequest{NodeID: leafNodeID})
	if err != nil {
		r.logger.Error("Failed to get status updates", "error", err)
		return nil, nil, err
	}

	return statusUpdates, conn, nil

}

func (r *Reconciler) ListenToWorkerStatusUpdates(ctx context.Context, workerID state.WorkerID) {
	for {
		statusUpdates, conn, err := r.getStatusUpdateStream(ctx, workerID)
		defer conn.Close()
		if err != nil {
			r.logger.Error("Failed to get status update stream", "workerID", workerID, "error", err)
			// Wait a bit before retrying
			time.Sleep(5 * time.Second)
			continue
		}

		for {
			update, err := statusUpdates.Recv()
			if err == io.EOF {
				r.logger.Debug("Status update stream closed", "workerID", workerID)
				break
			}
			if err != nil {
				r.logger.Error("Failed to receive status update", "error", err)
				break // Break inner loop to reconnect
			}

			r.logger.Debug("Received status update", "update", update)

			switch update.Type {
			case controllerpb.Type_TYPE_CONTAINER:
				switch update.Event {
				case controllerpb.Event_EVENT_TIMEOUT:
					r.handleContainerTimeout(workerID, state.FunctionID(update.FunctionId), state.InstanceID(update.InstanceId))
				case controllerpb.Event_EVENT_DOWN:
					r.handleContainerDown(workerID, state.FunctionID(update.FunctionId), state.InstanceID(update.InstanceId))
				default:
					r.logger.Warn("Received status update of unknown event", "event", update.Event)
				}
			default:
				r.logger.Warn("Received status update of unknown type", "type", update.Type)
			}
		}
	}
}

func (r *Reconciler) handleContainerTimeout(workerID state.WorkerID, functionID state.FunctionID, instanceID state.InstanceID) {
	r.logger.Debug("Container timed out", "instanceID", instanceID)

	err := r.workers.UpdateInstance(workerID, functionID, state.InstanceStateTimeout, state.Instance{InstanceID: instanceID})
	if err != nil {
		r.logger.Error("Failed to reconcile container timeout", "error", err)
	}
}

func (r *Reconciler) handleContainerDown(workerID state.WorkerID, functionID state.FunctionID, instanceID state.InstanceID) {
	r.logger.Debug("Container down", "instanceID", instanceID)

	err := r.workers.UpdateInstance(workerID, functionID, state.InstanceStateDown, state.Instance{InstanceID: instanceID})
	if err != nil {
		r.logger.Error("Failed to reconcile container down", "error", err)
	}
}
