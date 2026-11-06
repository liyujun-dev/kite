package cluster

import (
	"testing"

	"github.com/zxh326/kite/pkg/kube"
	"github.com/zxh326/kite/pkg/model"
	"k8s.io/client-go/kubernetes"
)

func stubServerVersion(t *testing.T, version string) func() {
	t.Helper()
	previous := serverVersion
	serverVersion = func(*ClientSet) (string, error) { return version, nil }
	return func() { serverVersion = previous }
}

func Test_shouldUpdateCluster(t *testing.T) {
	type args struct {
		cs      *ClientSet
		cluster *model.Cluster
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "enable/disable toggle, disable -> enable",
			args: args{
				cs:      nil,
				cluster: &model.Cluster{Name: "test", Enable: true},
			},
			want: true,
		},
		{
			name: "enable/disable toggle, enable -> disable",
			args: args{
				cs: &ClientSet{
					Name: "test",
				},
				cluster: &model.Cluster{Name: "test", Enable: false},
			},
			want: true,
		},
		{
			name: "disable cluster, keep disable",
			args: args{
				cs:      nil,
				cluster: &model.Cluster{Name: "test", Enable: false},
			},
			want: false,
		},
		{
			name: "invalid ClientSet(nil k8sClient), need update",
			args: args{
				cs: &ClientSet{
					Name:      "test",
					Version:   "v1.34.0",
					K8sClient: nil,
				},
				cluster: &model.Cluster{Name: "test", Enable: true},
			},
			want: true,
		},
		{
			name: "invalid ClientSet(nil k8sClient.ClientSet), need update",
			args: args{
				cs: &ClientSet{
					Name:    "test",
					Version: "v1.34.0",
					K8sClient: &kube.K8sClient{
						ClientSet: nil,
					},
				},
				cluster: &model.Cluster{Name: "test", Enable: true},
			},
			want: true,
		},
		{
			name: "k8s config change, need update",
			args: args{
				cs: &ClientSet{
					Name:    "test",
					Version: "v1.34.0",
					K8sClient: &kube.K8sClient{
						ClientSet: &kubernetes.Clientset{},
					},
					config: "test-config",
				},
				cluster: &model.Cluster{Name: "test", Enable: true, Config: model.SecretString("test-config-new")},
			},
			want: true,
		},
		{
			name: "prometheus url change, need update",
			args: args{
				cs: &ClientSet{
					Name:    "test",
					Version: "v1.34.0",
					K8sClient: &kube.K8sClient{
						ClientSet: &kubernetes.Clientset{},
					},
					prometheusURL: "test-prometheus-url",
				},
				cluster: &model.Cluster{Name: "test", Enable: true, PrometheusURL: "test-prometheus-url-new"},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldUpdateCluster(tt.args.cs, tt.args.cluster); got != tt.want {
				t.Errorf("shouldUpdateCluster() = %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("k8s version change, need update", func(t *testing.T) {
		defer stubServerVersion(t, "v1.34.0")()
		cs := &ClientSet{
			Name:    "test",
			Version: "v1.33.0",
			K8sClient: &kube.K8sClient{
				ClientSet: &kubernetes.Clientset{},
			},
		}
		cluster := &model.Cluster{Name: "test", Enable: true}

		if got := shouldUpdateCluster(cs, cluster); !got {
			t.Errorf("shouldUpdateCluster() = false, want true when the k8s version changed")
		}
	})

	t.Run("same, skip update", func(t *testing.T) {
		defer stubServerVersion(t, "v1.34.0")()
		cs := &ClientSet{
			Name:    "test",
			Version: "v1.34.0",
			K8sClient: &kube.K8sClient{
				ClientSet: &kubernetes.Clientset{},
			},
			config:        "test-config",
			prometheusURL: "test-prometheus-url",
		}
		cluster := &model.Cluster{
			Name:          "test",
			Enable:        true,
			Config:        model.SecretString("test-config"),
			PrometheusURL: "test-prometheus-url",
		}
		if got := shouldUpdateCluster(cs, cluster); got {
			t.Errorf("shouldUpdateCluster() = true, want false when everything matches")
		}
	})
}
