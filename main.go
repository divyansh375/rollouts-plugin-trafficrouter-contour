package main

import (
	rolloutsPlugin "github.com/argoproj/argo-rollouts/rollout/trafficrouting/plugin/rpc"
	goPlugin "github.com/hashicorp/go-plugin"
	"k8s.io/client-go/dynamic"

	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/argoproj/argo-rollouts/pkg/apis/rollouts/v1alpha1"
	pluginTypes "github.com/argoproj/argo-rollouts/utils/plugin/types"
	contourv1 "github.com/projectcontour/contour/apis/projectcontour/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"


	"os"

	"golang.org/x/exp/slog"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"


)

// handshakeConfigs are used to just do a basic handshake between
// a plugin and host. If the handshake fails, a user friendly error is shown.
// This prevents users from executing bad plugins or executing a plugin
// directory. It is a UX feature, not a security feature.
var handshakeConfig = goPlugin.HandshakeConfig{
	ProtocolVersion:  1,
	MagicCookieKey:   "ARGO_ROLLOUTS_RPC_PLUGIN",
	MagicCookieValue: "trafficrouter",
}

func main() {
	InitLogger()

	rpcPluginImp := &RpcPlugin{}

	//  pluginMap is the map of plugins we can dispense.
	var pluginMap = map[string]goPlugin.Plugin{
		"RpcTrafficRouterPlugin": &rolloutsPlugin.RpcTrafficRouterPlugin{Impl: rpcPluginImp},
	}

	slog.Info("the plugin is running")
	goPlugin.Serve(&goPlugin.ServeConfig{
		HandshakeConfig: handshakeConfig,
		Plugins:         pluginMap,
	})
}


// Type holds this controller type
const Type = "Contour"

type RpcPlugin struct {
	IsTest               bool
	dynamicClient        dynamic.Interface
	UpdatedMockHTTPProxy *contourv1.HTTPProxy
}

type ContourTrafficRouting struct {
	// HTTPProxy refers to the name of the HTTPProxy used to route traffic to the
	// service
	HTTPProxy string `json:"httpProxy" protobuf:"bytes,1,name=httpProxy"`
}

func (r *RpcPlugin) InitPlugin() (re pluginTypes.RpcError) {
	defer func() {
		if e := recover(); e != nil {
			re.ErrorString = e.(error).Error()
		}
	}()

	if r.IsTest {
		return
	}

	cfg := Must1(NewKubeConfig())
	r.dynamicClient = Must1(dynamic.NewForConfig(cfg))

	return
}
func (r *RpcPlugin) UpdateHash(rollout *v1alpha1.Rollout, canaryHash, stableHash string, additionalDestinations []v1alpha1.WeightDestination) pluginTypes.RpcError {
	return pluginTypes.RpcError{}
}

func (r *RpcPlugin) SetWeight(
	rollout *v1alpha1.Rollout,
	desiredWeight int32,
	additionalDestinations []v1alpha1.WeightDestination) (re pluginTypes.RpcError) {

	defer func() {
		if e := recover(); e != nil {
			re.ErrorString = e.(error).Error()
		}
	}()

	if rollout == nil || rollout.Spec.Strategy.Canary == nil ||
		rollout.Spec.Strategy.Canary.StableService == "" ||
		rollout.Spec.Strategy.Canary.CanaryService == "" {
		Must(errors.New("illegal parameter(s)"))
	}

	slog.Info("hello1")

	ctx := context.Background()

	ctr := ContourTrafficRouting{}
	
	Must(json.Unmarshal(rollout.Spec.Strategy.Canary.TrafficRouting.Plugins["argoproj-labs/contour"], &ctr))

	var httpProxy contourv1.HTTPProxy
	unstr := Must1(r.dynamicClient.Resource(contourv1.HTTPProxyGVR).Namespace(rollout.Namespace).Get(ctx, ctr.HTTPProxy, metav1.GetOptions{}))
	Must(runtime.DefaultUnstructuredConverter.FromUnstructured(unstr.UnstructuredContent(), &httpProxy))


	slog.Info("hello2")

	canarySvcName := rollout.Spec.Strategy.Canary.CanaryService
	stableSvcName := rollout.Spec.Strategy.Canary.StableService

	slog.Debug("the services name", slog.String("stable", stableSvcName), slog.String("canary", canarySvcName))

	// TODO: filter by condition(s)
	services := Must1(getServiceList(httpProxy.Spec.Routes))
	canarySvc := Must1(getService(canarySvcName, services))
	stableSvc := Must1(getService(stableSvcName, services))

	slog.Info("hello3")

	slog.Debug("old weight", slog.Int64("canary", canarySvc.Weight), slog.Int64("stable", stableSvc.Weight))

	canarySvc.Weight = int64(desiredWeight)
	stableSvc.Weight = 100 - canarySvc.Weight

	slog.Debug("new weight", slog.Int64("canary", canarySvc.Weight), slog.Int64("stable", stableSvc.Weight))

	m := Must1(runtime.DefaultUnstructuredConverter.ToUnstructured(&httpProxy))
	updated, err := r.dynamicClient.Resource(contourv1.HTTPProxyGVR).Namespace(rollout.Namespace).Update(ctx, &unstructured.Unstructured{Object: m}, metav1.UpdateOptions{})
	if err != nil {
		slog.Error("update the HTTPProxy is failed", slog.String("name", httpProxy.Name), slog.Any("err", err))
		Must(err)
	}

	slog.Info("hello4")

	if r.IsTest {

		proxy := contourv1.HTTPProxy{}
		Must(runtime.DefaultUnstructuredConverter.FromUnstructured(updated.UnstructuredContent(), &proxy))
		r.UpdatedMockHTTPProxy = &proxy
	}

	slog.Info("update HTTPProxy is successfully")
	return
}

func getService(name string, services []contourv1.Service) (*contourv1.Service, error) {
	var selected *contourv1.Service
	for i := 0; i < len(services); i++ {

		svc := &services[i]
		if svc.Name == name {
			selected = svc
			break
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("the service: %s is not found in HTTPProxy", name)
	}
	return selected, nil
}

func getServiceList(routes []contourv1.Route) ([]contourv1.Service, error) {
	for _, r := range routes {
		if r.Services == nil {
			continue
		}
		return r.Services, nil
	}
	return nil, errors.New("the services are not found in HTTPProxy")
}

func (r *RpcPlugin) SetHeaderRoute(rollout *v1alpha1.Rollout, headerRouting *v1alpha1.SetHeaderRoute) pluginTypes.RpcError {
	return pluginTypes.RpcError{}
}

func (r *RpcPlugin) SetMirrorRoute(rollout *v1alpha1.Rollout, setMirrorRoute *v1alpha1.SetMirrorRoute) pluginTypes.RpcError {
	return pluginTypes.RpcError{}
}

func (r *RpcPlugin) VerifyWeight(rollout *v1alpha1.Rollout, desiredWeight int32, additionalDestinations []v1alpha1.WeightDestination) (pluginTypes.RpcVerified, pluginTypes.RpcError) {
	return pluginTypes.Verified, pluginTypes.RpcError{}
}

func (r *RpcPlugin) RemoveManagedRoutes(rollout *v1alpha1.Rollout) pluginTypes.RpcError {
	return pluginTypes.RpcError{}
}

func (r *RpcPlugin) Type() string {
	return Type
}


func NewKubeConfig() (*rest.Config, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	// if you want to change the loading rules (which files in which order), you can do so here
	configOverrides := &clientcmd.ConfigOverrides{}
	// if you want to change override values or bind them to flags, there are methods to help you
	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)
	config, err := kubeConfig.ClientConfig()
	if err != nil {
		return nil, pluginTypes.RpcError{ErrorString: err.Error()}
	}
	return config, nil
}

func Must(err error) {
	if err != nil {
		panic(err)
	}
}

func Must1[T any](t T, err error) T {
	if err != nil {
		panic(err)
	}
	return t
}

func InitLogger() {
	lvl := &slog.LevelVar{}
	lvl.Set(slog.LevelDebug)
	opts := slog.HandlerOptions{
		Level: lvl,
	}

	attrs := []slog.Attr{
		slog.String("plugin", "trafficrouter"),
		slog.String("vendor", "contour"),
	}
	opts.NewTextHandler(os.Stderr).WithAttrs(attrs)

	l := slog.New(opts.NewTextHandler(os.Stderr).WithAttrs(attrs))
	slog.SetDefault(l)
}
