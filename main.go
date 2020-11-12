// SPDX-FileCopyrightText: 2017 SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gardener/aws-lb-readvertiser/controller"

	"k8s.io/client-go/informers"

	log "github.com/sirupsen/logrus"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// AWSReadvertiserOptions are the options for the AWSReadvertiser
type AWSReadvertiserOptions struct {
	endpointName           string
	kubeconfig             string
	elb                    string
	refreshPeriod          int
	controllerResyncPeriod int
}

func (a *AWSReadvertiserOptions) addFlags() {
	flag.StringVar(&a.kubeconfig, "kubeconfig", "", "kubeconfig")
	flag.StringVar(&a.elb, "elb-dns-name", "", "DNS name of elb")
	flag.IntVar(&a.refreshPeriod, "refresh-period", 5, "the period at which the Loadbalancer value is checked (in seconds)")
	flag.IntVar(&a.controllerResyncPeriod, "resync-period", 30, "the period at which the controller sync with the cache will happen (in seconds)")

	flag.Parse()
}

func (a *AWSReadvertiserOptions) validateFlags() error {
	if len(a.elb) == 0 {
		return fmt.Errorf("The DNS value for the ELB needs to be set properly")
	}

	// Check to see if the domain is a valid FQDN
	if !strings.HasSuffix(a.elb, ".") {
		a.elb = fmt.Sprintf("%s.", a.elb)
	}

	if a.refreshPeriod == 0 {
		log.Infof("The refresh period was not set, using default %d", a.refreshPeriod)
		return nil
	}

	if a.controllerResyncPeriod == 0 {
		log.Infof("The controller resync period was not set, using default %d", a.controllerResyncPeriod)
	}

	return nil
}

func (a *AWSReadvertiserOptions) initializeClient() (*kubernetes.Clientset, error) {
	var config *rest.Config

	switch {
	case len(a.kubeconfig) != 0:
		log.Infof("Using config from flag --kubeconfig %q", a.kubeconfig)
	default:
		a.kubeconfig, _ = os.LookupEnv("KUBECONFIG")
		log.Infof("Using config from $KUBECONFIG %q", a.kubeconfig)
	}

	config, err := clientcmd.BuildConfigFromFlags("", a.kubeconfig)
	if err != nil {
		return nil, err
	}

	return kubernetes.NewForConfig(config)
}

func (a *AWSReadvertiserOptions) run(ctx context.Context, client kubernetes.Interface) {
	var (
		sharedInformers             = informers.NewSharedInformerFactory(client, time.Duration(a.controllerResyncPeriod)*time.Second)
		awsLBReadvertiserController = controller.NewAWSLBEndpointsController(client, sharedInformers.Core().V1().Endpoints(), a.elb, "kubernetes")
		refreshTicker               = time.NewTicker(time.Duration(a.refreshPeriod) * time.Second)
	)

	go sharedInformers.Start(ctx.Done())
	awsLBReadvertiserController.Run(ctx, refreshTicker)
}

func main() {
	awsReadvertiser := new(AWSReadvertiserOptions)
	ctx, cancel := context.WithCancel(context.Background())

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)

	defer func() {
		signal.Stop(signalChan)
		cancel()
	}()

	go func() {
		select {
		case sig := <-signalChan:
			log.Printf("received signal: %s", sig.String())
			cancel()
		case <-ctx.Done():
		}
	}()

	awsReadvertiser.addFlags()
	if err := awsReadvertiser.validateFlags(); err != nil {
		log.Fatalf("Invalid flags, reason: %+v", err)
	}

	client, err := awsReadvertiser.initializeClient()
	if err != nil {
		log.Fatalf("failed to initialize client, error: %+v", err)
	}

	awsReadvertiser.run(ctx, client)
}
