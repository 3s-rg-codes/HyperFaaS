import { Trend } from 'k6/metrics';

export const callQueuedTimestampKey = 'callqueuedtimestamp';
export const gotResponseTimestampKey = 'gotresponsetimestamp';
export const instanceIdKey = 'instanceid';

export const callQueuedTimestamp = new Trend(callQueuedTimestampKey, true);
export const gotResponseTimestamp = new Trend(gotResponseTimestampKey, true);
export const instanceIdMetric = new Trend('instanceid');
