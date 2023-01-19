package main

import (
	"scaling_manager/cluster"
	"scaling_manager/config"
	fetch "scaling_manager/fetchmetrics"
	"scaling_manager/logger"
	"scaling_manager/provision"
	"scaling_manager/task"
	"scaling_manager/utils"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

var state = new(provision.State)

var log logger.LOG

func init() {
	log.Init("logger")
	log.Info.Println("Main module initialized")
}

func main() {
	// The following go routine will watch the changes inside config.yaml
	go fileWatch("config.yaml")
	// A periodic check if there is a change in master node to pick up incomplete provisioning
	go periodicProvisionCheck()
	// The polling interval is set to 5 minutes and can be configured.
	ticker := time.Tick(time.Duration(config.PollingInterval) * time.Second)
	for range ticker {
		state.GetCurrentState()
		// The recommendation and provisioning should only happen on master node
		if cluster.CheckIfMaster() && state.CurrentState == "normal" {
			// This function will be responsible for parsing the config file and fill in task_details struct.
			var task = new(task.TaskDetails)
			configStruct, err := config.GetConfig("config.yaml")
			if err != nil {
				log.Error.Println("The recommendation can not be made as there is an error in the validation of config file.")
				log.Error.Println(err.Error())
				continue
			}
			cfg := configStruct.ClusterDetails
			osClient := utils.CreateOsClient(cfg.OsCredentials.OsAdminUsername,
				cfg.OsCredentials.OsAdminPassword)
			// This function is responsible for fetching the metrics and pushing it to the index.
			// In starting we will call simulator to provide this details with current timestamp.
			fetch.FetchMetrics(osClient)
			task.Tasks = configStruct.TaskDetails
			// This function is responsible for evaluating the task and recommend.
			recommendationList := task.EvaluateTask(osClient)
			// This function is responsible for getting the recommendation and provision.
			provision.GetRecommendation(state, recommendationList, osClient)
		}
	}
}

// Input:
// Description: It periodically checks if the master node is changed and picks up if there was any ongoing provision operation
// Output:

func periodicProvisionCheck() {
	tick := time.Tick(time.Duration(config.PollingInterval) * time.Second)
	previousMaster := cluster.CheckIfMaster()
	for range tick {
		state.GetCurrentState()
		// Call a function which returns the current master node
		currentMaster := cluster.CheckIfMaster()
		if state.CurrentState != "normal" {
			if !(previousMaster) && currentMaster {
				configStruct, err := config.GetConfig("config.yaml")
				if err != nil {
					log.Warn.Println("Unable to get Config from GetConfig()", err)
					return
				}
				cfg := configStruct.ClusterDetails
				osClient := utils.CreateOsClient(cfg.OsCredentials.OsAdminUsername,
					cfg.OsCredentials.OsAdminPassword)
				if strings.Contains(state.CurrentState, "scaleup") {
					log.Debug.Println("Calling scaleOut")
					isScaledUp := provision.ScaleOut(cfg, state, osClient)
					if isScaledUp {
						log.Info.Println("Scaleup completed successfully")
					} else {
						// Add a retry mechanism
						log.Warn.Println("Scaleup failed")
					}
				} else if strings.Contains(state.CurrentState, "scaledown") {
					log.Debug.Println("Calling scaleIn")
					isScaledDown := provision.ScaleIn(cfg, state, osClient)
					if isScaledDown {
						log.Info.Println("Scaledown completed successfully")
					} else {
						// Add a retry mechanism
						log.Warn.Println("Scaledown failed")
					}
				}
			}
		}
		// Update the previousMaster for next loop
		previousMaster = currentMaster
	}
}

func fileWatch(filePath string) {
	//Adding file watcher to detect the change in configuration
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Error.Println("ERROR: ", err)
	}
	defer watcher.Close()
	done := make(chan bool)

	//A go routine that keeps checking for change in configuration
	go func() {
		for {
			select {
			// watch for events
			case event := <-watcher.Events:
				//If there is change in config then clear recommendation queue
				//clearRecommendationQueue()
				log.Warn.Println("EVENT!", event)
				log.Warn.Println("The recommendation queue will be cleared.")
			case err := <-watcher.Errors:
				log.Error.Println("ERROR in file watcher: ", err)
			}
		}
	}()

	// Adding fsnotify watcher to keep track of the changes in config file
	if err := watcher.Add(filePath); err != nil {
		log.Error.Println("ERROR:", err)
	}

	<-done
}
