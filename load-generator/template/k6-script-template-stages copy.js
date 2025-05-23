import grpc from 'k6/net/grpc';
import { check } from 'k6';

// K6 GRPC client
const client = new grpc.Client();
client.load(['../config'], '__PROTO_FILE__');

// K6 Options
export const options = {
	scenarios: {
		scenario_t1: {
			executor: 'ramping-arrival-rate',
			startTime: '0s', // scenario_one starts at 0s
			exec: 'callServiceA_param1',
			preAllocatedVUs: 10,
			stages: [
				{ duration: '30s', target: 10 },  // Ramp from 0 to 10 iters/s in 30s
				{ duration: '1m', target: 100 }, // Ramp from 10 to 100 iters/s in 1m
				{ duration: '30s', target: 100 }, // Stay at 100 iters/s for 30s
				{ duration: '30s', target: 0 },   // Ramp down from 100 to 0 iters/s in 30s
			],
		},
		scenario_t2: {
			executor: 'ramping-arrival-rate',
			startTime: '0s', // scenario_two starts at 0s
			exec: 'callServiceB_param1',
			preAllocatedVUs: 10,
			stages: [
				{ duration: '20s', target: 5 }, // Ramp from 0 to 5 iters/s in 20s
				{ duration: '40s', target: 5 }, // Stay at 5 iters/s for 40s
				{ duration: '20s', target: 0 }, // Ramp down from 5 to 0 iters/s in 20s
			],
		},
		scenario_t3: {
			executor: 'constant-arrival-rate',
			startTime: '150s', // scenario_three starts after scenario_one is finished (150s = stage durations added up)
			exec: 'callServiceA_param2',
			preAllocatedVUs: 10,
			startRate: 100,
			stages: [
				{ duration: '10s', target: 1000 }, // Ramp from startRate (100) to 1000 iters/s in 10s
				{ duration: '2m', target: 1000 },  // Stay at 1000 iters/s for 2m
				{ duration: '5s', target: 100 },   // Ramp down from 1000 to 100 iters/s in 30s
			],
		}
	},
};

// Payloads
const payloads = {
	'serviceA_param1': 1,
	'serviceB_param1': 2,
	'serviceA_param2': 3,
};

// Service call functions
export function callServiceA_param1() {
	client.connect('localhost:50052', { plaintext: true });

	const response = client.invoke('something/ServiceA', payloads['serviceA_param1']);

	check(response, {
		'status is OK': (r) => r && r.status === grpc.StatusOK
	});
}

export function callServiceB_param1() {
	client.connect('localhost:50052', { plaintext: true });

	const response = client.invoke('something/ServiceB', payloads['serviceB_param1']);

	check(response, {
		'status is OK': (r) => r && r.status === grpc.StatusOK
	});
}

export function callServiceA_param2() {
	client.connect('localhost:50052', { plaintext: true });

	const response = client.invoke('something/ServiceA', payloads['serviceA_param2']);

	check(response, {
		'status is OK': (r) => r && r.status === grpc.StatusOK
	});
}
