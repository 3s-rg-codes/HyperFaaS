import grpc from 'k6/net/grpc';
import { check } from 'k6';
import { randomBytes } from 'k6/crypto';
import encoding from 'k6/encoding';
import http from 'k6/http';
import { randomSeed } from 'k6';

// Add our custom metrics
import { Trend } from 'k6/metrics';
const callQueuedTimestampKey = 'callqueuedtimestamp';
const gotResponseTimestampKey = 'gotresponsetimestamp';
const instanceIdKey = 'instanceid';
const callQueuedTimestamp = new Trend(callQueuedTimestampKey, true);
const gotResponseTimestamp = new Trend(gotResponseTimestampKey, true);
const instanceIdMetric = new Trend('instanceid');


// Helper functions
function getRandomInt(min, max) {
  min = Math.ceil(min);
  max = Math.floor(max);
  return Math.floor(Math.random() * (max - min + 1)) + min;
}

function parseK6Duration(durationStr) {
  if (typeof durationStr !== 'string') return 0;
  let totalSeconds = 0;
  const parts = durationStr.match(/(\d+h)?(\d+m)?(\d+s)?/);
  if (!parts) return 0;
  if (parts[1]) totalSeconds += parseInt(parts[1].slice(0, -1)) * 3600; // hours
  if (parts[2]) totalSeconds += parseInt(parts[2].slice(0, -1)) * 60;   // minutes
  if (parts[3]) totalSeconds += parseInt(parts[3].slice(0, -1));        // seconds
  return totalSeconds;
}

// Environment variable configuration with defaults
const config = {
  // Global Configuration
  workloadSeed: parseInt(__ENV.WORKLOAD_SEED) || Date.now(),
  totalTestDuration: __ENV.TOTAL_TEST_DURATION || "60s",
  minPreallocatedVus: parseInt(__ENV.MIN_PREALLOCATED_VUS) || 10,
  maxPreallocatedVus: parseInt(__ENV.MAX_PREALLOCATED_VUS) || 50,
  minMaxVus: parseInt(__ENV.MIN_MAX_VUS) || 20,
  maxMaxVus: parseInt(__ENV.MAX_MAX_VUS) || 100,
  rampingStartRateMin: parseInt(__ENV.RAMPING_START_RATE_MIN) || 1,
  rampingStartRateMax: parseInt(__ENV.RAMPING_START_RATE_MAX) || 5,

  // BFS Configuration
  BFS_MIN_SCENARIOS: parseInt(__ENV.BFS_MIN_SCENARIOS) || 1,
  BFS_MAX_SCENARIOS: parseInt(__ENV.BFS_MAX_SCENARIOS) || 3,
  BFS_CONSTANT_SCENARIOS_RATIO: parseFloat(__ENV.BFS_CONCallQueuedTimestampSTANT_SCENARIOS_RATIO) || 0.5,
  BFS_CONSTANT_RATE_MIN: parseInt(__ENV.BFS_CONSTANT_RATE_MIN) || 5,
  BFS_CONSTANT_RATE_MAX: parseInt(__ENV.BFS_CONSTANT_RATE_MAX) || 20,
  BFS_BURST_TARGET_RATE_MIN: parseInt(__ENV.BFS_BURST_TARGET_RATE_MIN) || 10,
  BFS_BURST_TARGET_RATE_MAX: parseInt(__ENV.BFS_BURST_TARGET_RATE_MAX) || 40,
  BFS_IMAGE_TAG: __ENV.BFS_IMAGE_TAG || 'hyperfaas-bfs-json:latest',

  // Echo Configuration
  ECHO_MIN_SCENARIOS: parseInt(__ENV.ECHO_MIN_SCENARIOS) || 1,
  ECHO_MAX_SCENARIOS: parseInt(__ENV.ECHO_MAX_SCENARIOS) || 3,
  ECHO_CONSTANT_SCENARIOS_RATIO: parseFloat(__ENV.ECHO_CONSTANT_SCENARIOS_RATIO) || 0.5,
  ECHO_CONSTANT_RATE_MIN: parseInt(__ENV.ECHO_CONSTANT_RATE_MIN) || 5,
  ECHO_CONSTANT_RATE_MAX: parseInt(__ENV.ECHO_CONSTANT_RATE_MAX) || 20,
  ECHO_BURST_TARGET_RATE_MIN: parseInt(__ENV.ECHO_BURST_TARGET_RATE_MIN) || 10,
  ECHO_BURST_TARGET_RATE_MAX: parseInt(__ENV.ECHO_BURST_TARGET_RATE_MAX) || 40,
  ECHO_IMAGE_TAG: __ENV.ECHO_IMAGE_TAG || 'hyperfaas-echo:latest',

  // Thumbnailer Configuration
  THUMBNAILER_MIN_SCENARIOS: parseInt(__ENV.THUMBNAILER_MIN_SCENARIOS) || 1,
  THUMBNAILER_MAX_SCENARIOS: parseInt(__ENV.THUMBNAILER_MAX_SCENARIOS) || 3,
  THUMBNAILER_CONSTANT_SCENARIOS_RATIO: parseFloat(__ENV.THUMBNAILER_CONSTANT_SCENARIOS_RATIO) || 0.5,
  THUMBNAILER_CONSTANT_RATE_MIN: parseInt(__ENV.THUMBNAILER_CONSTANT_RATE_MIN) || 5,
  THUMBNAILER_CONSTANT_RATE_MAX: parseInt(__ENV.THUMBNAILER_CONSTANT_RATE_MAX) || 20,
  THUMBNAILER_BURST_TARGET_RATE_MIN: parseInt(__ENV.THUMBNAILER_BURST_TARGET_RATE_MIN) || 10,
  THUMBNAILER_BURST_TARGET_RATE_MAX: parseInt(__ENV.THUMBNAILER_BURST_TARGET_RATE_MAX) || 40,
  THUMBNAILER_IMAGE_TAG: __ENV.THUMBNAILER_IMAGE_TAG || 'hyperfaas-thumbnailer-json:latest',
};

randomSeed(config.workloadSeed);

const client = new grpc.Client();
client.load(['./config'], 'common.proto', 'leaf.proto');

let bfsFunctionId;
let echoFunctionId;
let thumbnailerFunctionId;

// An image to use for the thumbnailer function
const imageUrl = 'https://picsum.photos/200/300';
let imageDataB64;

// Setup function to create the functions before the test
export function setup() {
  client.connect('localhost:50050', {
    plaintext: true
  });

  // Create BFS function
  const bfsCreateResponse = client.invoke('leaf.Leaf/CreateFunction', {
    image_tag: { tag: 'hyperfaas-bfs-json:latest' },
    config: {
      memory: 256 * 1024 * 1024, // 256MB
      cpu: {
        period: 100000,
        quota: 50000
      }
    }
  });
  check(bfsCreateResponse, {
    'No error when creating BFS function': (r) => {
      if (r && r.error) {
        console.log('Error creating BFS function:', r.error);
        return false;
      }
      return true;
    },
    'BFS function created successfully': (r) => r && r.message && r.message.functionID && r.message.functionID.id
  });
  bfsFunctionId = bfsCreateResponse.message.functionID.id;
  console.log("bfsFunctionId=", bfsFunctionId);

  // Create Echo function
  const echoCreateResponse = client.invoke('leaf.Leaf/CreateFunction', {
    image_tag: { tag: 'hyperfaas-echo:latest' },
    config: {
      memory: 128 * 1024 * 1024, // 128MB
      cpu: {
        period: 100000,
        quota: 25000
      }
    }
  });
  check(echoCreateResponse, {
    'Echo function created successfully': (r) => r && r.message && r.message.functionID && r.message.functionID.id
  });
  echoFunctionId = echoCreateResponse.message.functionID.id;
  console.log("echoFunctionId=", echoFunctionId);

  // Create Thumbnailer function
  const thumbnailerCreateResponse = client.invoke('leaf.Leaf/CreateFunction', {
    image_tag: { tag: 'hyperfaas-thumbnailer-json:latest' },
    config: {
      memory: 512 * 1024 * 1024, // 512MB
      cpu: {  
        period: 100000,
        quota: 50000
      }
    }
  });
  check(thumbnailerCreateResponse, {
    'Thumbnailer function created successfully': (r) => r && r.message && r.message.functionID && r.message.functionID.id
  });
  thumbnailerFunctionId = thumbnailerCreateResponse.message.functionID.id;
  console.log("thumbnailerFunctionId=", thumbnailerFunctionId);

  // Download the image
  imageDataB64 = encoding.b64encode(http.get(imageUrl).body);
  return { bfsFunctionId, echoFunctionId, thumbnailerFunctionId };
}

// BFS function execution
export function bfsFunction(setupData) {
    client.connect('localhost:50050', {
        plaintext: true
      });
  
  const size = Math.floor(Math.random() * (100 - 100 + 1)) + 100;
  
  // Create input data structure
  const inputData = {
    Size: size,
    // Randomly decide whether to include a seed
    ...(Math.random() > 0.5 && { Seed: Math.floor(Math.random() * 1000000) })
  };

  // Convert to JSON string and then to base64
  const data = encoding.b64encode(JSON.stringify(inputData));

  const response = client.invoke('leaf.Leaf/ScheduleCall', {
    functionID: { id: setupData.bfsFunctionId },
    data: data
  });

  if (response.error) {
    console.log('Error scheduling BFS function:', response.error);
    return;
  }

  callQueuedTimestamp.add(isoToMs(response.trailers[callQueuedTimestampKey]));
  gotResponseTimestamp.add(isoToMs(response.trailers[gotResponseTimestampKey]));
  instanceIdMetric.add(0, {instanceId: response.trailers[instanceIdKey][0]});

  client.close();
}

// Echo function execution
export function echoFunction(setupData) {
  client.connect('localhost:50050', {
    plaintext: true
  });
  const data = encoding.b64encode(randomBytes(Math.floor(Math.random() * (2048 - 512 + 1)) + 512));
  const response = client.invoke('leaf.Leaf/ScheduleCall', {
    functionID: { id: setupData.echoFunctionId },
    data: data
  });
  if (response.error) {
    console.log('Error scheduling Echo function:', response.error);
    return;
  }
  callQueuedTimestamp.add(isoToMs(response.trailers[callQueuedTimestampKey]));
  gotResponseTimestamp.add(isoToMs(response.trailers[gotResponseTimestampKey]));
  instanceIdMetric.add(0, {instanceId: response.trailers[instanceIdKey][0]});

  // check that there is no error and that the data that was sent is the same as the data that was received
  /* check(response, {
    'No error when scheduling echo function': (r) => {
        if (r && r.error) {
            console.log('Error scheduling echo function:', r.error);
            return false;
        }
        return true;
    },
    'Data sent is the same as the data that was received': (r) => r && r.message && r.message.data === data
  }); */

  client.close();
}

// Thumbnailer function execution
export function thumbnailerFunction(setupData) {
  client.connect('localhost:50050', {
    plaintext: true
  });
  // Create input data structure with random dimensions
  const width = Math.floor(Math.random() * (200 - 50 + 1)) + 50;  // Random width between 50 and 200
  const height = Math.floor(Math.random() * (200 - 50 + 1)) + 50; // Random height between 50 and 200
  
  const inputData = {
    image: imageDataB64,
    width: width,
    height: height
  };

  // Convert to JSON string and then to base64
  const data = encoding.b64encode(JSON.stringify(inputData));

  const response = client.invoke('leaf.Leaf/ScheduleCall', {
    functionID: { id: setupData.thumbnailerFunctionId },
    data: data
  });
  if (response.error) {
    console.log('Error scheduling Thumbnailer function:', response.error);
    return;
  }
  callQueuedTimestamp.add(isoToMs(response.trailers[callQueuedTimestampKey]));
  gotResponseTimestamp.add(isoToMs(response.trailers[gotResponseTimestampKey]));
  instanceIdMetric.add(0, {instanceId: response.trailers[instanceIdKey][0]});

  client.close();
}

function isoToMs(isoString) {
    return new Date(isoString).getTime();
}

// Function configuration for scenario generation
const functionsToProcess = [
  {
    name: 'bfs',
    exec: 'bfsFunction',
    configPrefix: 'BFS',
    imageTag: config.BFS_IMAGE_TAG
  },
  {
    name: 'echo',
    exec: 'echoFunction',
    configPrefix: 'ECHO',
    imageTag: config.ECHO_IMAGE_TAG
  },
  {
    name: 'thumbnailer',
    exec: 'thumbnailerFunction',
    configPrefix: 'THUMBNAILER',
    imageTag: config.THUMBNAILER_IMAGE_TAG
  }
];

// Generate dynamic scenarios
const totalTestDurationSeconds = parseK6Duration(config.totalTestDuration);
let generatedScenarios = {};

for (const funcInfo of functionsToProcess) {
  const prefix = funcInfo.configPrefix;
  const minScenarios = config[`${prefix}_MIN_SCENARIOS`];
  const maxScenarios = config[`${prefix}_MAX_SCENARIOS`];
  
  // Skip if function is disabled or no duration
  if (maxScenarios === 0 || totalTestDurationSeconds === 0) {
    console.log(`Skipping ${funcInfo.name} scenarios (disabled or no duration)`);
    continue;
  }

  const numScenarios = getRandomInt(minScenarios, maxScenarios);
  const avgScenarioDurationSec = totalTestDurationSeconds / numScenarios;
  let currentFunctionStartTimeSec = 0;

  for (let i = 0; i < numScenarios; i++) {
    const scenarioName = `${funcInfo.name}_${i}`;
    const preAllocatedVUs = getRandomInt(config.minPreallocatedVus, config.maxPreallocatedVus);
    let maxVUs = getRandomInt(config.minMaxVus, config.maxMaxVus);
    if (maxVUs < preAllocatedVUs) maxVUs = preAllocatedVUs;

    const isConstantRate = Math.random() < config[`${prefix}_CONSTANT_SCENARIOS_RATIO`];
    const executorType = isConstantRate ? 'constant-arrival-rate' : 'ramping-arrival-rate';

    let scenarioDetails = {
      exec: funcInfo.exec,
      startTime: `${Math.floor(currentFunctionStartTimeSec)}s`,
      preAllocatedVUs: preAllocatedVUs,
      maxVUs: maxVUs,
      tags: { 
        scenario_group: funcInfo.name, 
        type: executorType, 
        image_tag: funcInfo.imageTag 
      },
      timeUnit: '1s',
    };

    if (isConstantRate) {
      scenarioDetails = {
        ...scenarioDetails,
        executor: 'constant-arrival-rate',
        duration: `${Math.ceil(avgScenarioDurationSec)}s`,
        rate: getRandomInt(
          config[`${prefix}_CONSTANT_RATE_MIN`],
          config[`${prefix}_CONSTANT_RATE_MAX`]
        )
      };
    } else {
      scenarioDetails = {
        ...scenarioDetails,
        executor: 'ramping-arrival-rate',
        startRate: getRandomInt(config.rampingStartRateMin, config.rampingStartRateMax),
        stages: [{
          target: getRandomInt(
            config[`${prefix}_BURST_TARGET_RATE_MIN`],
            config[`${prefix}_BURST_TARGET_RATE_MAX`]
          ),
          duration: `${Math.ceil(avgScenarioDurationSec)}s`
        }]
      };
    }

    generatedScenarios[scenarioName] = scenarioDetails;
    currentFunctionStartTimeSec += avgScenarioDurationSec;
  }
}

config.persistGeneration = __ENV.PERSIST_GENERATION === 'true' || false;
// Store the persistence data in a variable accessible to handleSummary
const persistenceData = {
  metadata: {
    seed: config.workloadSeed,
    totalDuration: config.totalTestDuration,
    generatedAt: new Date().toISOString(),
    configuration: config,
    /* bfsFunctionId: "bfsFunctionId",
    echoFunctionId: echoFunctionId,
    thumbnailerFunctionId: thumbnailerFunctionId */
  },
  scenarios: generatedScenarios
};

export const options = {
  scenarios: generatedScenarios,
  systemTags: ['error', 'group', 'proto', 'scenario', 'service', 'subproto', 'extra_tags', 'metadata', 'vu', 'iter']
};

export function handleSummary(data) {
  if (config.persistGeneration) {
    return {
      'stdout': JSON.stringify(persistenceData, null, 2)
    };
  }
  return {};
}