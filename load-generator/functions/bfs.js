import {
  callQueuedTimestamp,
  gotResponseTimestamp,
  instanceIdMetric,
  callQueuedTimestampKey,
  gotResponseTimestampKey,
  instanceIdKey,
  leafGotRequestTimestamp,
  leafScheduledCallTimestamp,
  leafGotRequestTimestampKey,
  leafScheduledCallTimestampKey,
  functionParametersMetric,
  timeout,
  error
} from '../metrics.js';
import { isoToMs } from '../utils.js'

import grpc from 'k6/net/grpc';
import { check } from 'k6';
import encoding from 'k6/encoding';

const client = new grpc.Client();
client.load(['./config'], 'common.proto', 'leaf.proto');

export const bfsConfig = {
  // BFS Configuration
  BFS_MIN_SCENARIOS: parseInt(__ENV.BFS_MIN_SCENARIOS) || 1,
  BFS_MAX_SCENARIOS: parseInt(__ENV.BFS_MAX_SCENARIOS) || 3,
  BFS_CONSTANT_SCENARIOS_RATIO: parseFloat(__ENV.BFS_CONCallQueuedTimestampSTANT_SCENARIOS_RATIO) || 0.5,
  BFS_CONSTANT_RATE_MIN: parseInt(__ENV.BFS_CONSTANT_RATE_MIN) || 5,
  BFS_CONSTANT_RATE_MAX: parseInt(__ENV.BFS_CONSTANT_RATE_MAX) || 20,
  BFS_BURST_TARGET_RATE_MIN: parseInt(__ENV.BFS_BURST_TARGET_RATE_MIN) || 10,
  BFS_BURST_TARGET_RATE_MAX: parseInt(__ENV.BFS_BURST_TARGET_RATE_MAX) || 40,
  BFS_IMAGE_TAG: __ENV.BFS_IMAGE_TAG || 'hyperfaas-bfs-json:latest',
}

export function bfsSetup(client) {
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
  const bfsFunctionId = bfsCreateResponse.message.functionID.id;
  console.log("bfsFunctionId=", bfsFunctionId);
  return bfsFunctionId;
}

export function bfsFunction(setupData) {
  const bfsFunctionId = setupData.bfs;
    client.connect(setupData.address, {
    plaintext: true
  });

  const size = 100;

  // Create input data structure
  const inputData = {
    Size: size,
    // Randomly decide whether to include a seed
    ...(Math.random() > 0.5 && { Seed: Math.floor(Math.random() * 1000000) })
  };

  // Convert to JSON string and then to base64
  const data = encoding.b64encode(JSON.stringify(inputData));

  const response = client.invoke('leaf.Leaf/ScheduleCall', {
    functionID: { id: bfsFunctionId },
    data: data,
  },
  {
    timeout: setupData.timeout
  }
);

  if (response.status === grpc.StatusDeadlineExceeded) {
    timeout.add(Date.now());
    return;
  }

  if (response.error) {
    error.add(Date.now());
    return;
  }
  callQueuedTimestamp.add(isoToMs(response.trailers[callQueuedTimestampKey]));
  gotResponseTimestamp.add(isoToMs(response.trailers[gotResponseTimestampKey]));
  instanceIdMetric.add(0, { instanceId: response.trailers[instanceIdKey][0] });
  leafGotRequestTimestamp.add(isoToMs(response.trailers[leafGotRequestTimestampKey]));
  leafScheduledCallTimestamp.add(isoToMs(response.trailers[leafScheduledCallTimestampKey]));
  functionParametersMetric.add(0, { functionParameters: JSON.stringify(inputData) });
  client.close();
}
