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
  functionProcessingTime,
  functionProcessingTimeKey,
  timeout,
  error
} from '../metrics.js';
import { getRandomInt, isoToMs } from '../utils.js'
import grpc from 'k6/net/grpc';
import { randomBytes } from 'k6/crypto';
import encoding from 'k6/encoding';

const client = new grpc.Client();
client.load(['./config'], 'common.proto', 'leaf.proto');

export function echoFunction(setupData) {
  const echoFunctionId = setupData.echo;
  /* if (__ITER === 0) {
    client.connect(setupData.address, {
      plaintext: true
    });
  } */
  client.connect(setupData.address, {
    plaintext: true
  });
  
  const data = encoding.b64encode(randomBytes(getRandomInt(512, 2048)));
  const response = client.invoke('leaf.Leaf/ScheduleCall', {
    functionID: { id: echoFunctionId },
    data: data,
  },
  {
    timeout: setupData.timeout
  });

  if (response.status === grpc.StatusDeadlineExceeded) {
    timeout.add(Date.now());
    client.close();
    return;
  }
  
  if (response.error) {
    error.add(Date.now());
    client.close();
    return;
  }
  
  callQueuedTimestamp.add(isoToMs(response.trailers[callQueuedTimestampKey]));
  gotResponseTimestamp.add(isoToMs(response.trailers[gotResponseTimestampKey]));
  instanceIdMetric.add(0, { instanceId: response.trailers[instanceIdKey][0] });
  leafGotRequestTimestamp.add(isoToMs(response.trailers[leafGotRequestTimestampKey]));
  leafScheduledCallTimestamp.add(isoToMs(response.trailers[leafScheduledCallTimestampKey]));
  functionProcessingTime.add(isoToMs(response.trailers[functionProcessingTimeKey]));
  //functionParametersMetric.add(0, { functionParameters: JSON.stringify(data) });
  client.close();
} 