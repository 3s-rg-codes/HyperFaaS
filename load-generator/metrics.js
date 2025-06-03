import { Trend } from 'k6/metrics';
export const functionTimeout = "10s"
export const callQueuedTimestampKey = 'callqueuedtimestamp';
export const gotResponseTimestampKey = 'gotresponsetimestamp';
export const instanceIdKey = 'instanceid';
export const functionProcessingTimeKey = 'functionprocessingtime';
export const leafGotRequestTimestampKey = 'leafgotrequesttimestamp';
export const leafScheduledCallTimestampKey = 'leafscheduledcalltimestamp';


export const callQueuedTimestamp = new Trend(callQueuedTimestampKey, true);
export const gotResponseTimestamp = new Trend(gotResponseTimestampKey, true);
export const instanceIdMetric = new Trend(instanceIdKey);
export const leafGotRequestTimestamp = new Trend(leafGotRequestTimestampKey, true);
export const leafScheduledCallTimestamp = new Trend(leafScheduledCallTimestampKey, true);
export const functionProcessingTime = new Trend(functionProcessingTimeKey);
export const timeout = new Trend("timeout", true);
export const error = new Trend("error", true);
// for some reason if we use a function the metrics are not exported, so don't try it..