package resourcehandler

import (
	"fmt"

	"github.com/armosec/armoapi-go/armotypes"
	"github.com/armosec/k8s-interface/workloadinterface"
	"github.com/armosec/opa-utils/reporthandling"
	"k8s.io/apimachinery/pkg/version"

	"github.com/armosec/k8s-interface/k8sinterface"
	"github.com/armosec/kubescape/v2/core/cautils"
	"github.com/armosec/kubescape/v2/core/cautils/logger"
	"github.com/armosec/kubescape/v2/core/cautils/logger/helpers"
)

// FileResourceHandler handle resources from files and URLs
type FileResourceHandler struct {
	inputPatterns    []string
	registryAdaptors *RegistryAdaptors
}

func NewFileResourceHandler(inputPatterns []string, registryAdaptors *RegistryAdaptors) *FileResourceHandler {
	k8sinterface.InitializeMapResourcesMock() // initialize the resource map
	return &FileResourceHandler{
		inputPatterns:    inputPatterns,
		registryAdaptors: registryAdaptors,
	}
}

func (fileHandler *FileResourceHandler) GetResources(sessionObj *cautils.OPASessionObj, designator *armotypes.PortalDesignator) (*cautils.K8SResources, map[string]workloadinterface.IMetadata, *cautils.ArmoResources, error) {

	logger.L().Info("Accessing local objects")
	cautils.StartSpinner()

	// build resources map
	// map resources based on framework required resources: map["/group/version/kind"][]<k8s workloads ids>
	k8sResources := setK8sResourceMap(sessionObj.Policies)
	allResources := map[string]workloadinterface.IMetadata{}
	workloadIDToSource := make(map[string]reporthandling.Source, 0)
	armoResources := &cautils.ArmoResources{}

	workloads := []workloadinterface.IMetadata{}

	// load resource from local file system
	sourceToWorkloads, err := cautils.LoadResourcesFromFiles(fileHandler.inputPatterns[0])
	if err != nil {
		return nil, allResources, nil, err
	}
	for source, ws := range sourceToWorkloads {
		workloads = append(workloads, ws...)
		for i := range ws {
			workloadIDToSource[ws[i].GetID()] = reporthandling.Source{RelativePath: source}
		}
	}
	logger.L().Debug("files found in local storage", helpers.Int("files", len(sourceToWorkloads)), helpers.Int("workloads", len(workloads)))

	// addCommitData(fileHandler.inputPatterns[0], workloadIDToSource)

	if len(workloads) == 0 {
		return nil, allResources, nil, fmt.Errorf("empty list of workloads - no workloads found")
	}
	logger.L().Debug("files found in git repo", helpers.Int("files", len(sourceToWorkloads)), helpers.Int("workloads", len(workloads)))

	sessionObj.ResourceSource = workloadIDToSource

	// map all resources: map["/group/version/kind"][]<k8s workloads>
	mappedResources := mapResources(workloads)

	// save only relevant resources
	for i := range mappedResources {
		if _, ok := (*k8sResources)[i]; ok {
			ids := []string{}
			for j := range mappedResources[i] {
				ids = append(ids, mappedResources[i][j].GetID())
				allResources[mappedResources[i][j].GetID()] = mappedResources[i][j]
			}
			(*k8sResources)[i] = ids
		}
	}

	if err := fileHandler.registryAdaptors.collectImagesVulnerabilities(k8sResources, allResources, armoResources); err != nil {
		logger.L().Warning("failed to collect images vulnerabilities", helpers.Error(err))
	}

	cautils.StopSpinner()
	logger.L().Success("Accessed to local objects")

	return k8sResources, allResources, armoResources, nil

}

func (fileHandler *FileResourceHandler) GetClusterAPIServerInfo() *version.Info {
	return nil
}

// build resources map
func mapResources(workloads []workloadinterface.IMetadata) map[string][]workloadinterface.IMetadata {

	allResources := map[string][]workloadinterface.IMetadata{}
	for i := range workloads {
		groupVersionResource, err := k8sinterface.GetGroupVersionResource(workloads[i].GetKind())
		if err != nil {
			// TODO - print warning
			continue
		}

		if k8sinterface.IsTypeWorkload(workloads[i].GetObject()) {
			w := workloadinterface.NewWorkloadObj(workloads[i].GetObject())
			if groupVersionResource.Group != w.GetGroup() || groupVersionResource.Version != w.GetVersion() {
				// TODO - print warning
				continue
			}
		}
		resourceTriplets := k8sinterface.JoinResourceTriplets(groupVersionResource.Group, groupVersionResource.Version, groupVersionResource.Resource)
		if r, ok := allResources[resourceTriplets]; ok {
			allResources[resourceTriplets] = append(r, workloads[i])
		} else {
			allResources[resourceTriplets] = []workloadinterface.IMetadata{workloads[i]}
		}
	}
	return allResources

}

func addCommitData(input string, workloadIDToSource map[string]reporthandling.Source) {
	giRepo, err := cautils.NewLocalGitRepository(input)
	if err != nil {
		return
	}
	for k := range workloadIDToSource {
		sourceObj := workloadIDToSource[k]
		lastCommit, err := giRepo.GetFileLastCommit(sourceObj.RelativePath)
		if err != nil {
			continue
		}
		sourceObj.LastCommit = reporthandling.LastCommit{
			Hash:           lastCommit.SHA,
			Date:           lastCommit.Author.Date,
			CommitterName:  lastCommit.Author.Name,
			CommitterEmail: lastCommit.Author.Email,
			Message:        lastCommit.Message,
		}
		workloadIDToSource[k] = sourceObj
	}
}
