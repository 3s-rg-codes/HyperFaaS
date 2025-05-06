export function buildScenarios(functionName, payloads) {
    const scenarios = {};
    let currentStartTime = 0;

    payloads.forEach((entry) => {
        const { seconds, rps, vus } = entry;

        const scenarioName = `${functionName}_t${seconds}`;

        scenarios[scenarioName] = {
            // general options
            executor: 'constant-arrival-rate',
            startTime: `${currentStartTime}s`,
            exec: `${functionName}_exec`,
            // executor options
            duration: `${seconds}s`,
            rate: rps,
            preAllocatedVUs: vus,
            timeUnit: '1s',
            maxVUs: vus,
        };

        currentStartTime += seconds;
    });

    return scenarios;
}
