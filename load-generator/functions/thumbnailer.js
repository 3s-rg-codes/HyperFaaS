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
import { getRandomInt, isoToMs } from '../utils.js'
import grpc from 'k6/net/grpc';
import { check } from 'k6';
import encoding from 'k6/encoding';
import http from 'k6/http';

const client = new grpc.Client();
client.load(['./config'], 'common.proto', 'leaf.proto');
const imageUrl = 'https://picsum.photos/200/300';

export const thumbnailerConfig = {
  // Thumbnailer Configuration
  THUMBNAILER_MIN_SCENARIOS: parseInt(__ENV.THUMBNAILER_MIN_SCENARIOS) || 1,
  THUMBNAILER_MAX_SCENARIOS: parseInt(__ENV.THUMBNAILER_MAX_SCENARIOS) || 3,
  THUMBNAILER_CONSTANT_SCENARIOS_RATIO: parseFloat(__ENV.THUMBNAILER_CONSTANT_SCENARIOS_RATIO) || 0.5,
  THUMBNAILER_CONSTANT_RATE_MIN: parseInt(__ENV.THUMBNAILER_CONSTANT_RATE_MIN) || 5,
  THUMBNAILER_CONSTANT_RATE_MAX: parseInt(__ENV.THUMBNAILER_CONSTANT_RATE_MAX) || 20,
  THUMBNAILER_BURST_TARGET_RATE_MIN: parseInt(__ENV.THUMBNAILER_BURST_TARGET_RATE_MIN) || 10,
  THUMBNAILER_BURST_TARGET_RATE_MAX: parseInt(__ENV.THUMBNAILER_BURST_TARGET_RATE_MAX) || 40,
  THUMBNAILER_IMAGE_TAG: __ENV.THUMBNAILER_IMAGE_TAG || 'hyperfaas-thumbnailer-json:latest',
}

export function thumbnailerSetup(client) {
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
  const thumbnailerFunctionId = thumbnailerCreateResponse.message.functionID.id;
  console.log("thumbnailerFunctionId=", thumbnailerFunctionId);

  // Download the image
  const imageDataB64 = encoding.b64encode(http.get(imageUrl).body);
  return { thumbnailerFunctionId, imageDataB64 };
}

// Thumbnailer function execution
export function thumbnailerFunction(setupData) {
  const { thumbnailerFunctionId, imageDataB64 } = setupData.thumbnailer;
  client.connect(setupData.address, {
    plaintext: true
  });
  // Create input data structure with random dimensions
  const width = getRandomInt(50,200)  // Random width between 50 and 200
  const height = getRandomInt(50, 200); // Random height between 50 and 200

  const inputData = {
    image: imageDataB64,
    width: width,
    height: height
  };

  // Convert to JSON string and then to base64
  const data = encoding.b64encode(JSON.stringify(inputData));

  const response = client.invoke('leaf.Leaf/ScheduleCall', {
    functionID: { id: thumbnailerFunctionId },
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
