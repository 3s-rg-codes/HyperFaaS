import grpc from 'k6/net/grpc';
import { check, sleep } from 'k6';
import exec from 'k6/execution';

// K6 Options
export const options = {
    scenarios: __SCENARIOS__
};

// K6 GRPC client
const client = new grpc.Client();
client.load(['../config'], '__PROTO_FILE__');

// Payloads
const payloads = __PAYLOADS__;

// K6 exec function
export function __FUNCTION_NAME___exec() {

    const currentScenario = exec.scenario.name;
    const iteration = parseInt(currentScenario.split('_t')[1], 10) - 1;

    const entry = payloads[iteration];

    client.connect('localhost:50052', { plaintext: true });

    const response = client.invoke('__SERVICE_FN__', entry.payload);

    check(response, {
        'status is OK': (r) => r && r.status === grpc.StatusOK
    });

    client.close();
    sleep(1);
}

// K6 main function
export default function () {
    __FUNCTION_NAME___exec();
}
