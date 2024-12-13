package statsEndpoints

type workerStatus struct {
	functionData map[string][]functionInformation
	//map from workerID to functionInformation for all functionIDs on a worker
}

type functionInformation struct {
	functionID          string
	runningInstanceList []instanceInformation //different lists for running and idle funtioninstances
	idleInstanceList    []instanceInformation
}

type instanceInformation struct {
	instanceID        string
	active            bool //if the instance is running or idle
	timeSinceLastWork int  //time since the last request was processed to know if to kill
	timeRunning       int  //time since the instance was started to know if to kill
}

func scrapeStats() {
	//scrape stats from all workers
	//for now we only scrape status, but metrics should also be added in future

	//We need to make a grpc call in GR 1 and get the info from a worker

	//Then write that info into the workerStatus struct

	//At same time GR 2 is running to receive any incoming requests to start / call / stop an instance
	//Once a request comes in GR 2 checks the workerStatus "table" to see which worker to assign it to

	//It then does so and sends the command to the corresponding worker

	//Approach 1: GR 2 modifies the table itself
	// -in this case GR 1 only updates the table for stop and error events
	// -GR 2 updates the table for start and call events

	//Approach 2: GR 2 only gives orders and GR 1 then changes the table once the given statusEvents
	//start rolling in
	// -in this case GR 1 updates the table for all events
}
