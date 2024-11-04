/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

//+kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;update

package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// PodReconciler reconciles a Pod object
type PodReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func sendTeamsNotification(pod corev1.Pod, restartCount int32) error {
	// TODO: hacerlo parametrizable para enviar a otro channel
	// webhookURL := "https://url-de-microsoft-teams"
	webhookURL := os.Getenv("WEBHOOK_URL")
	// webhookURL, exists := os.LookupEnv("WEBHOOK_URL")
	// if !exists {
	// 	fmt.Println("WEBHOOK_URL is not set")
	// 	return nil
	// }

	// Construir la Adaptive Card
	adaptiveCard := map[string]interface{}{
		"type": "message",
		"attachments": []map[string]interface{}{
			{
				"contentType": "application/vnd.microsoft.card.adaptive",
				"content": map[string]interface{}{
					"$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
					"type":    "AdaptiveCard",
					"version": "1.3",
					"body": []map[string]interface{}{
						{
							"type":   "TextBlock",
							"text":   "🚨 **Pod Reiniciando**",
							"size":   "Large",
							"weight": "Bolder",
							"color":  "Attention",
						},
						{
							"type": "FactSet",
							"facts": []map[string]string{
								{"title": "Pod Name:", "value": pod.Name},
								{"title": "Namespace:", "value": pod.Namespace},
								{"title": "Restart Count:", "value": fmt.Sprintf("%d", restartCount)},
							},
						},
					},
				},
			},
		},
	}

	payloadBytes, err := json.Marshal(adaptiveCard)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", webhookURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// if resp.StatusCode != http.StatusOK {
	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("received non-202 response code: %d", resp.StatusCode)
	}

	return nil
}

// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=pods/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core,resources=pods/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Pod object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/reconcile
func (r *PodReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	fmt.Println("######## Reconcile Pod ########")

	// _ = log.FromContext(ctx)
	logger := log.FromContext(ctx)
	fmt.Println("logger", logger)

	// Filtrar por el namespace "mynamespace"
	namespaceToWatch, exists := os.LookupEnv("NAMESPACE_NAME")
	if !exists {
		fmt.Println("NAMESPACE_NAME is not set")
		return ctrl.Result{}, nil
	}

	// if req.Namespace != "default" { // TODO: hacer parametrizable el namespace
	if req.Namespace != namespaceToWatch {
		return ctrl.Result{}, nil
	}

	var pod corev1.Pod
	if err := r.Get(ctx, req.NamespacedName, &pod); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	fmt.Println("Pod Statuses", pod.Status.ContainerStatuses)
	// Revisar el estado de los contenedores
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.RestartCount > 0 {
			fmt.Println("======== Pod Reiniciando =========")
			fmt.Println("Namespace:", pod.Namespace)
			fmt.Println("Nombre del pod:", pod.Name)
			fmt.Println("RestartCount:", cs.RestartCount)
			// Opcional: Resetear el contador después de imprimir
			// Nota: El contador no puede ser reseteado manualmente, es solo para monitoreo

			// Obtener el último RestartCount notificado desde las anotaciones
			lastNotifiedRestartCount := int32(0)
			if val, ok := pod.Annotations["pod-restart-controller/last-restart-count"]; ok {
				fmt.Sscanf(val, "%d", &lastNotifiedRestartCount)
				fmt.Println("Último RestartCount notificado:", lastNotifiedRestartCount)
			} else {
				// Inicializar las anotaciones si no existen
				fmt.Println("Inicializando las anotaciones")
				if pod.Annotations == nil {
					pod.Annotations = make(map[string]string)
				}
			}

			fmt.Printf("Es mayor RestartCount > lastNotifiedRestartCount? %d > %d\n", cs.RestartCount, lastNotifiedRestartCount)
			if cs.RestartCount > lastNotifiedRestartCount {
				// Enviar notificación al webhook de Microsoft Teams
				err := sendTeamsNotification(pod, cs.RestartCount)
				if err != nil {
					logger.Error(err, "Error al enviar la notificación al webhook")
				}

				// MsTeams workflow type "Post to a channel when a webhook request is received"
				// Actualizar la anotación con el nuevo RestartCount
				pod.Annotations["pod-restart-controller/last-restart-count"] = fmt.Sprintf("%d", cs.RestartCount)
				if err := r.Update(ctx, &pod); err != nil {
					logger.Error(err, "Error al actualizar las anotaciones del Pod")
					return ctrl.Result{}, err
				} else {
					fmt.Println("Anotaciones actualizadas")
				}
			}

			break
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PodReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Pod{}).
		Named("pod").
		Complete(r)
}
