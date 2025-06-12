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
import { randomBytes } from 'k6/crypto';
import encoding from 'k6/encoding';

const client = new grpc.Client();
client.load(['./config'], 'common.proto', 'leaf.proto');

export const echoConfig = {
  // Echo Configuration
  ECHO_MIN_SCENARIOS: parseInt(__ENV.ECHO_MIN_SCENARIOS) || 1,
  ECHO_MAX_SCENARIOS: parseInt(__ENV.ECHO_MAX_SCENARIOS) || 3,
  ECHO_CONSTANT_SCENARIOS_RATIO: parseFloat(__ENV.ECHO_CONSTANT_SCENARIOS_RATIO) || 0.5,
  ECHO_CONSTANT_RATE_MIN: parseInt(__ENV.ECHO_CONSTANT_RATE_MIN) || 5,
  ECHO_CONSTANT_RATE_MAX: parseInt(__ENV.ECHO_CONSTANT_RATE_MAX) || 20,
  ECHO_BURST_TARGET_RATE_MIN: parseInt(__ENV.ECHO_BURST_TARGET_RATE_MIN) || 10,
  ECHO_BURST_TARGET_RATE_MAX: parseInt(__ENV.ECHO_BURST_TARGET_RATE_MAX) || 40,
  ECHO_IMAGE_TAG: __ENV.ECHO_IMAGE_TAG || 'hyperfaas-echo:latest',
}

export function echoSetup(client) {
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
  const echoFunctionId = echoCreateResponse.message.functionID.id;
  console.log("echoFunctionId=", echoFunctionId);

  return echoFunctionId;
}

// Echo function execution
export function echoFunction(setupData) {
  const echoFunctionId = setupData.echo;
  client.connect('localhost:50050', {
    plaintext: true
  });
  const data = encoding.b64encode(randomBytes(getRandomInt(512, 2048)));
  const response = client.invoke('leaf.Leaf/ScheduleCall', {
    functionID: { id: echoFunctionId },
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
  functionParametersMetric.add(0, { functionParameters: JSON.stringify(data) });
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
