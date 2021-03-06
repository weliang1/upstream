package dockerhelper

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"

	"github.com/blang/semver"
	docker "github.com/fsouza/go-dockerclient"
	"github.com/golang/glog"

	starterrors "github.com/openshift/origin/pkg/bootstrap/docker/errors"
	"github.com/openshift/origin/pkg/cmd/util/pullprogress"
)

const openShiftInsecureCIDR = "172.30.0.0/16"

// Helper provides utility functions to help with Docker
type Helper struct {
	client *docker.Client
}

// NewHelper creates a new Helper
func NewHelper(client *docker.Client) *Helper {
	return &Helper{
		client: client,
	}
}

type RegistryConfig struct {
	InsecureRegistryCIDRs []string
}

func getRegistryConfig(env *docker.Env) (*RegistryConfig, error) {
	for _, entry := range *env {
		if !strings.HasPrefix(entry, "RegistryConfig=") {
			continue
		}
		glog.V(5).Infof("Found RegistryConfig entry: %s", entry)
		value := strings.TrimPrefix(entry, "RegistryConfig=")
		config := &RegistryConfig{}
		err := json.Unmarshal([]byte(value), config)
		if err != nil {
			glog.V(2).Infof("Error unmarshalling RegistryConfig: %v", err)
			return nil, err
		}
		glog.V(5).Infof("Unmarshalled registry config to %#v", config)
		return config, nil
	}
	return nil, nil
}

func hasCIDR(cidr string, listOfCIDRs []string) bool {
	glog.V(5).Infof("Looking for %q in %#v", cidr, listOfCIDRs)
	for _, candidate := range listOfCIDRs {
		if candidate == cidr {
			glog.V(5).Infof("Found %q", cidr)
			return true
		}
	}
	glog.V(5).Infof("Did not find %q", cidr)
	return false
}

// HasInsecureRegistryArg checks whether the docker daemon is configured with
// the appropriate insecure registry argument
func (h *Helper) HasInsecureRegistryArg() (bool, error) {
	glog.V(5).Infof("Retrieving Docker daemon info")
	env, err := h.client.Info()
	if err != nil {
		glog.V(2).Infof("Could not retrieve Docker info: %v", err)
		return false, err
	}
	glog.V(5).Infof("Docker daemon info: %#v", env)
	registryConfig, err := getRegistryConfig(env)
	if err != nil {
		return false, err
	}
	return hasCIDR(openShiftInsecureCIDR, registryConfig.InsecureRegistryCIDRs), nil
}

// Version returns the Docker version
func (h *Helper) Version() (*semver.Version, error) {
	glog.V(5).Infof("Retrieving Docker version")
	env, err := h.client.Version()
	if err != nil {
		glog.V(2).Infof("Error retrieving version: %v", err)
		return nil, err
	}
	glog.V(5).Infof("Docker version results: %#v", env)
	versionStr := env.Get("Version")
	if len(versionStr) == 0 {
		return nil, errors.New("did not get a version")
	}
	glog.V(5).Infof("Version: %s", versionStr)
	dockerVersion, err := semver.Parse(versionStr)
	if err != nil {
		glog.V(2).Infof("Error parsing Docker version %q", versionStr)
		return nil, err
	}
	return &dockerVersion, nil
}

// CheckAndPull checks whether a Docker image exists. If not, it pulls it.
func (h *Helper) CheckAndPull(image string, out io.Writer) error {
	glog.V(5).Infof("Inspecting Docker image %q", image)
	imageMeta, err := h.client.InspectImage(image)
	if err == nil {
		glog.V(5).Infof("Image %q found: %#v", image, imageMeta)
		return nil
	}
	if err != docker.ErrNoSuchImage {
		return starterrors.NewError("unexpected error inspecting image %s", image).WithCause(err)
	}
	glog.V(5).Infof("Image %q not found. Pulling", image)
	fmt.Fprintf(out, "Pulling image %s\n", image)
	extracting := false
	var outputStream io.Writer
	writeProgress := func(r *pullprogress.ProgressReport) {
		if extracting {
			return
		}
		if r.Downloading == 0 && r.Waiting == 0 && r.Extracting > 0 {
			fmt.Fprintf(out, "Extracting\n")
			extracting = true
			return
		}
		plural := "s"
		if r.Downloading == 1 {
			plural = " "
		}
		fmt.Fprintf(out, "Downloading %d layer%s (%3.0f%%)", r.Downloading, plural, r.DownloadPct)
		if r.Waiting > 0 {
			fmt.Fprintf(out, ", %d waiting\n", r.Waiting)
		} else {
			fmt.Fprintf(out, "\n")
		}
	}
	if !glog.V(5) {
		outputStream = pullprogress.NewPullProgressWriter(writeProgress)
	} else {
		outputStream = out
	}
	err = h.client.PullImage(docker.PullImageOptions{
		Repository:    image,
		RawJSONStream: bool(!glog.V(5)),
		OutputStream:  outputStream,
	}, docker.AuthConfiguration{})
	if err != nil {
		return starterrors.NewError("error pulling Docker image %s", image).WithCause(err)
	}
	fmt.Fprintf(out, "Image pull comlete\n")
	return nil
}

// GetContainerState returns whether a container exists and if it does whether it's running
func (h *Helper) GetContainerState(id string) (exists, running bool, err error) {
	var container *docker.Container
	glog.V(5).Infof("Inspecting docker container %q", id)
	container, err = h.client.InspectContainer(id)
	if err != nil {
		if _, notFound := err.(*docker.NoSuchContainer); notFound {
			glog.V(5).Infof("Container %q was not found", id)
			err = nil
			return
		}
		glog.V(5).Infof("An error occurred inspecting container %q: %v", id, err)
		return
	}
	exists = true
	running = container.State.Running
	glog.V(5).Infof("Container inspect result: %#v", container)
	glog.V(5).Infof("Container exists = %v, running = %v", exists, running)
	return
}

// RemoveContainer removes the container with the given id
func (h *Helper) RemoveContainer(id string) error {
	glog.V(5).Infof("Removing container %q", id)
	err := h.client.RemoveContainer(docker.RemoveContainerOptions{
		ID: id,
	})
	if err != nil {
		return starterrors.NewError("cannot delete container %s", id).WithCause(err)
	}
	glog.V(5).Infof("Removed container %q", id)
	return nil
}

// HostIP returns the IP of the Docker host if connecting via TCP
func (h *Helper) HostIP() string {
	// By default, if the Docker client uses tcp, then use the Docker daemon's address
	endpoint := h.client.Endpoint()
	u, err := url.Parse(endpoint)
	if err == nil && (u.Scheme == "tcp" || u.Scheme == "http" || u.Scheme == "https") {
		glog.V(2).Infof("Using the Docker host %s for the server IP", endpoint)
		if host, _, serr := net.SplitHostPort(u.Host); serr == nil {
			return host
		}
		return u.Host
	}
	glog.V(5).Infof("Cannot use Docker endpoint (%s) because it is not using one of the following protocols: tcp, http, https", endpoint)
	return ""
}

func (h *Helper) StopAndRemoveContainer(container string) error {
	err := h.client.StopContainer(container, 10)
	if err != nil {
		glog.V(2).Infof("Cannot stop container %s: %v", container, err)
	}
	return h.RemoveContainer(container)
}

func (h *Helper) ListContainerNames() ([]string, error) {
	containers, err := h.client.ListContainers(docker.ListContainersOptions{All: true})
	if err != nil {
		return nil, err
	}
	names := []string{}
	for _, c := range containers {
		names = append(names, c.Names[0])
	}
	return names, nil
}
