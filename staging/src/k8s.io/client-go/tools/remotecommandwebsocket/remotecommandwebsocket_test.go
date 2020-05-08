package remotecommandwebsocket

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	//"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	//
	// Uncomment to load all auth plugins
	// _ "k8s.io/client-go/plugin/pkg/client/auth"
	//
	// Or uncomment to load specific auth plugins
	// _ "k8s.io/client-go/plugin/pkg/client/auth/azure"
	// _ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	_ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
	// _ "k8s.io/client-go/plugin/pkg/client/auth/openstack"
	//"k8s.io/client-go/tools/remotecommandwebsocket"
)

func TestMyCommand(t *testing.T) {
	t.Log("hello")
	t.Log("hell2")

	var kubeconfig *string
	if home := homeDir(); home != "" {
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	}
	flag.Parse()

	config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)

	apiServerURL, err := url.Parse(config.Host)
	fmt.Println(apiServerURL.Scheme)
	apiServerURL.Scheme = "wss"
	t.Log(apiServerURL)
	if err != nil {
		panic(err.Error())
	}
	// creates the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}

	pods, err := clientset.CoreV1().Pods("openunison").Get(context.TODO(), "openunison-orchestra-78858c5f4-8bnkb", metav1.GetOptions{})
	if err != nil {
		panic(err.Error())
	}

	t.Log(pods.GetObjectMeta().GetSelfLink())

	apiServerURL.Path = pods.GetObjectMeta().GetSelfLink() + "/exec"

	queryString := apiServerURL.Query()
	queryString.Add("command", "bash")
	//queryString.Add("command", "'test'")

	queryString.Add("sterr", "true")
	queryString.Add("stdout", "true")
	queryString.Add("stdin", "true")
	queryString.Add("tty", "true")

	apiServerURL.RawQuery = queryString.Encode()

	t.Log(apiServerURL)

	exec, err := NewWebSocketExecutor(config, apiServerURL)
	if err != nil {
		panic(err.Error())
	}

	t.Log(exec)

	streamOptions := StreamOptions{
		Stdin:             os.Stdin,
		Stdout:            os.Stdout,
		Stderr:            os.Stderr,
		Tty:               true,
		TerminalSizeQueue: nil,
	}

	err = exec.Stream(streamOptions)
	if err != nil {
		t.Error(err)
	}
}

func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return os.Getenv("USERPROFILE") // windows
}
