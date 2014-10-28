package build

import (
	"errors"
	"testing"
	"time"

	kapi "github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	kclient "github.com/GoogleCloudPlatform/kubernetes/pkg/client"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/labels"
	"github.com/openshift/origin/pkg/build/api"
)

type okOsClient struct{}

func (_ *okOsClient) ListBuilds(ctx kapi.Context, selector labels.Selector) (*api.BuildList, error) {
	return &api.BuildList{}, nil
}

func (_ *okOsClient) UpdateBuild(kapi.Context, *api.Build) (*api.Build, error) {
	return &api.Build{}, nil
}

type errOsClient struct{}

func (_ *errOsClient) ListBuilds(ctx kapi.Context, selector labels.Selector) (*api.BuildList, error) {
	return &api.BuildList{}, errors.New("ListBuild error!")
}

func (_ *errOsClient) UpdateBuild(ctx kapi.Context, build *api.Build) (*api.Build, error) {
	return &api.Build{}, errors.New("UpdateBuild error!")
}

type okStrategy struct{}

func (_ *okStrategy) CreateBuildPod(build *api.Build) (*kapi.Pod, error) {
	return &kapi.Pod{}, nil
}

type errStrategy struct{}

func (_ *errStrategy) CreateBuildPod(build *api.Build) (*kapi.Pod, error) {
	return nil, errors.New("CreateBuildPod error!")
}

type errKubeClient struct {
	kclient.Fake
}

func (_ *errKubeClient) CreatePod(ctx kapi.Context, pod *kapi.Pod) (*kapi.Pod, error) {
	return &kapi.Pod{}, errors.New("CreatePod error!")
}

func (_ *errKubeClient) GetPod(ctx kapi.Context, name string) (*kapi.Pod, error) {
	return &kapi.Pod{}, errors.New("GetPod error!")
}

type errExistsKubeClient struct {
	kclient.Fake
}

func (_ *errExistsKubeClient) CreatePod(ctx kapi.Context, pod *kapi.Pod) (*kapi.Pod, error) {
	return &kapi.Pod{}, errors.New("CreatePod already exists error!")
}

type okKubeClient struct {
	kclient.Fake
}

func (_ *okKubeClient) GetPod(ctx kapi.Context, name string) (*kapi.Pod, error) {
	return &kapi.Pod{
		CurrentState: kapi.PodState{Status: kapi.PodTerminated},
	}, nil
}

type termKubeClient struct {
	kclient.Fake
}

func (_ *termKubeClient) GetPod(ctx kapi.Context, name string) (*kapi.Pod, error) {
	return &kapi.Pod{
		CurrentState: kapi.PodState{
			Status: kapi.PodTerminated,
			Info: kapi.PodInfo{
				"container1": kapi.ContainerStatus{
					State: kapi.ContainerState{
						Termination: &kapi.ContainerStateTerminated{ExitCode: 1},
					},
				},
			},
		},
	}, nil
}

func TestSynchronizeBuildNew(t *testing.T) {
	ctrl, build, ctx := setup()
	build.Status = api.BuildNew
	status, err := ctrl.synchronize(ctx, build)
	if err != nil {
		t.Errorf("Unexpected error: %s!", err.Error())
	}
	if status != api.BuildPending {
		t.Errorf("Expected BuildPending, got %s!", status)
	}
}

func TestSynchronizeBuildPendingFailedCreateBuildPod(t *testing.T) {
	ctrl, build, ctx := setup()
	ctrl.buildStrategies[api.DockerBuildType] = &errStrategy{}
	build.Status = api.BuildPending
	status, err := ctrl.synchronize(ctx, build)
	if err == nil {
		t.Error("Expected error, but none happened!")
	}
	if status != api.BuildFailed {
		t.Errorf("Expected BuildFailed, got %s!", status)
	}
}

func TestSynchronizeBuildPendingFailedCreatePod(t *testing.T) {
	ctrl, build, ctx := setup()
	ctrl.kubeClient = &errKubeClient{}
	build.Status = api.BuildPending
	status, err := ctrl.synchronize(ctx, build)
	if err == nil {
		t.Error("Expected error, but none happened!")
	}
	if status != api.BuildFailed {
		t.Errorf("Expected BuildFailed, got %s!", status)
	}
}

func TestSynchronizeBuildPendingFailedCreatePodAlreadyExists(t *testing.T) {
	ctrl, build, ctx := setup()
	ctrl.kubeClient = &errExistsKubeClient{}
	build.Status = api.BuildPending
	build.CreationTimestamp.Time = time.Now()
	status, err := ctrl.synchronize(ctx, build)
	if err == nil {
		t.Error("Expected error, but none happened!")
	}
	if status != api.BuildPending {
		t.Errorf("Expected BuildPending, got %s!", status)
	}
}

func TestSynchronizeBuildPending(t *testing.T) {
	ctrl, build, ctx := setup()
	build.Status = api.BuildPending
	status, err := ctrl.synchronize(ctx, build)
	if err != nil {
		t.Errorf("Unexpected error: %s!", err.Error())
	}
	if status != api.BuildRunning {
		t.Errorf("Expected BuildRunning, got %s!", status)
	}
}

func TestSynchronizeBuildRunningTimedOut(t *testing.T) {
	ctrl, build, ctx := setup()
	build.Status = api.BuildRunning
	build.CreationTimestamp.Time = time.Date(0, 0, 0, 0, 0, 0, 0, time.UTC)
	status, err := ctrl.synchronize(ctx, build)
	if err == nil {
		t.Error("Expected error, but none happened!")
	}
	if status != api.BuildFailed {
		t.Errorf("Expected BuildFailed, got %s!", status)
	}
}

func TestSynchronizeBuildRunningFailedGetPod(t *testing.T) {
	ctrl, build, ctx := setup()
	ctrl.kubeClient = &errKubeClient{}
	build.Status = api.BuildRunning
	build.CreationTimestamp.Time = time.Now()
	status, err := ctrl.synchronize(ctx, build)
	if err == nil {
		t.Error("Expected error, but none happened!")
	}
	if status != api.BuildRunning {
		t.Errorf("Expected BuildRunning, got %s!", status)
	}
}

func TestSynchronizeBuildRunningPodRunning(t *testing.T) {
	ctrl, build, ctx := setup()
	build.Status = api.BuildRunning
	build.CreationTimestamp.Time = time.Now()
	status, err := ctrl.synchronize(ctx, build)
	if err != nil {
		t.Errorf("Unexpected error, got %s!", err.Error())
	}
	if status != api.BuildRunning {
		t.Errorf("Expected BuildRunning, got %s!", status)
	}
}

func TestSynchronizeBuildRunningPodTerminationExitCode(t *testing.T) {
	ctrl, build, ctx := setup()
	ctrl.kubeClient = &termKubeClient{}
	build.Status = api.BuildRunning
	build.CreationTimestamp.Time = time.Now()
	status, err := ctrl.synchronize(ctx, build)
	if err != nil {
		t.Errorf("Unexpected error, got %s!", err.Error())
	}
	if status != api.BuildFailed {
		t.Errorf("Expected BuildFailed, got %s!", status)
	}
}

func TestSynchronizeBuildRunningPodTerminated(t *testing.T) {
	ctrl, build, ctx := setup()
	ctrl.kubeClient = &okKubeClient{}
	build.Status = api.BuildRunning
	build.CreationTimestamp.Time = time.Now()
	status, err := ctrl.synchronize(ctx, build)
	if err != nil {
		t.Errorf("Unexpected error, got %s!", err.Error())
	}
	if status != api.BuildComplete {
		t.Errorf("Expected BuildComplete, got %s!", status)
	}
}

func TestSynchronizeBuildComplete(t *testing.T) {
	ctrl, build, ctx := setup()
	build.Status = api.BuildComplete
	status, err := ctrl.synchronize(ctx, build)
	if err != nil {
		t.Errorf("Unexpected error, got %s!", err.Error())
	}
	if status != api.BuildComplete {
		t.Errorf("Expected BuildComplete, got %s!", status)
	}
}

func TestSynchronizeBuildFailed(t *testing.T) {
	ctrl, build, ctx := setup()
	build.Status = api.BuildFailed
	status, err := ctrl.synchronize(ctx, build)
	if err != nil {
		t.Errorf("Unexpected error, got %s!", err.Error())
	}
	if status != api.BuildFailed {
		t.Errorf("Expected BuildFailed, got %s!", status)
	}
}

func TestSynchronizeBuildError(t *testing.T) {
	ctrl, build, ctx := setup()
	build.Status = api.BuildError
	status, err := ctrl.synchronize(ctx, build)
	if err != nil {
		t.Errorf("Unexpected error, got %s!", err.Error())
	}
	if status != api.BuildError {
		t.Errorf("Expected BuildError, got %s!", status)
	}
}

func TestSynchronizeBuildUnknownStatus(t *testing.T) {
	ctrl, build, ctx := setup()
	build.Status = "unknownBuildStatus"
	status, err := ctrl.synchronize(ctx, build)
	if err == nil {
		t.Error("Expected error, but none happened!")
	}
	if status != api.BuildError {
		t.Errorf("Expected BuildError, got %s!", status)
	}
}

func setup() (buildController *BuildController, build *api.Build, ctx kapi.Context) {
	buildController = &BuildController{
		buildStrategies: map[api.BuildType]BuildJobStrategy{
			api.DockerBuildType: &okStrategy{},
		},
		kubeClient: &kclient.Fake{},
		timeout:    1000,
	}
	build = &api.Build{
		TypeMeta: kapi.TypeMeta{
			ID: "dataBuild",
		},
		Source: api.BuildSource{
			Git: &api.GitBuildSource{
				URI: "http://my.build.com/the/build/Dockerfile",
			},
		},
		Input: api.BuildInput{
			ImageTag: "repository/dataBuild",
		},
		Status: api.BuildNew,
		PodID:  "-the-pod-id",
		Labels: map[string]string{
			"name": "dataBuild",
		},
	}
	ctx = kapi.NewDefaultContext()
	return
}
