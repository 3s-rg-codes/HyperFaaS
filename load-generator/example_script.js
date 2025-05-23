import grpc from 'k6/net/grpc';
import { check } from 'k6';
import { randomBytes } from 'k6/crypto';
import encoding from 'k6/encoding';
import http from 'k6/http';

// Add our custom metrics
import { Trend } from 'k6/metrics';
const callQueuedTimestampKey = 'callqueuedtimestamp';
const gotResponseTimestampKey = 'gotresponsetimestamp';
const callQueuedTimestamp = new Trend(callQueuedTimestampKey, true);
const gotResponseTimestamp = new Trend(gotResponseTimestampKey, true);


const client = new grpc.Client();
client.load(['./config'], 'common.proto', 'leaf.proto');

// Durations
const warmupDuration = "10s";
const steadyDuration = "10s";
const peakDuration = "10s";
const echoDuration = "10s";
const thumbnailerDuration = "10s";

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

  // Download the image
  imageDataB64 = encoding.b64encode(http.get(imageUrl).body);

  console.log('Function IDs:', { bfsFunctionId, echoFunctionId, thumbnailerFunctionId });
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
  callQueuedTimestamp.add(isoToMs(response.trailers[callQueuedTimestampKey]));
  gotResponseTimestamp.add(isoToMs(response.trailers[gotResponseTimestampKey]));
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
  callQueuedTimestamp.add(isoToMs(response.trailers[callQueuedTimestampKey]));
  gotResponseTimestamp.add(isoToMs(response.trailers[gotResponseTimestampKey]));

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
  callQueuedTimestamp.add(isoToMs(response.trailers[callQueuedTimestampKey]));
  gotResponseTimestamp.add(isoToMs(response.trailers[gotResponseTimestampKey]));
  client.close();
}

export const options = {
    "scenarios" : {
  // Warm-up phase for BFS
  "bfs_warmup": {
    executor: 'ramping-arrival-rate',
    startTime: '0s',
    startRate: 5,
    timeUnit: '1s',
    preAllocatedVUs: 20,
    maxVUs: 50,
    stages: [
      { duration: warmupDuration, target: 20 }
    ],
    exec: 'bfsFunction',
    tags: {scenario: 'bfs_warmup', image_tag: 'hyperfaas-bfs-json:latest'}
  },
  // Steady load for BFS
  "bfs_steady": {
    executor: 'constant-arrival-rate',
    startTime: warmupDuration,
    duration: steadyDuration,
    rate: 20,
    timeUnit: '1s',
    preAllocatedVUs: 40,
    maxVUs: 60,
    exec: 'bfsFunction',
    tags: {scenario: 'bfs_steady', image_tag: 'hyperfaas-bfs-json:latest'}
  },
  // Peak load for BFS
  "bfs_peak": {
    executor: 'ramping-arrival-rate',
    startTime: steadyDuration,
    startRate: 20,
    timeUnit: '1s',
    preAllocatedVUs: 50,
    maxVUs: 100,
    stages: [
      { duration: peakDuration, target: 40 }
    ],
    exec: 'bfsFunction',
    tags: {scenario: 'bfs_peak', image_tag: 'hyperfaas-bfs-json:latest'}
  },
  // Echo constant load (runs parallel to BFS)
  "echo_steady": {
    executor: 'constant-arrival-rate',
    startTime: '0s',
    duration: echoDuration,
    rate: 30,
    timeUnit: '1s',
    preAllocatedVUs: 60,
    maxVUs: 80,
    exec: 'echoFunction',
    tags: {scenario: 'echo_steady', image_tag: 'hyperfaas-echo:latest'}
  },
  // Thumbnailer constant load (runs parallel to BFS)
  "thumbnailer_steady": {
    executor: 'constant-arrival-rate',
    startTime: '0s',
    duration: thumbnailerDuration,
    rate: 10,
    timeUnit: '1s',
    preAllocatedVUs: 20,
    maxVUs: 40,
    exec: 'thumbnailerFunction',
    tags: {scenario: 'thumbnailer_steady', image_tag: 'hyperfaas-thumbnailer-json:latest'}
  }
  }
};

function isoToMs(isoString) {
    return new Date(isoString).getTime();
}