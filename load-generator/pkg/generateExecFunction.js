export function createExecFunction(functionName, serviceFn) {

    return `export function ${functionName}_exec() {

    const currentScenario = exec.scenario.name;
    const iteration = parseInt(currentScenario.split('_t')[1], 10) - 1;

    const entry = payloads[iteration];

    client.connect('localhost:50052', { plaintext: true });

    const response = client.invoke('${serviceFn}', entry.payload);

    check(response, {
        'status is OK': (r) => r && r.status === grpc.StatusOK
    });

    client.close();
    sleep(1);
}`;
}
