import fs from 'fs';

import { loadConfig } from './pkg/loadConfig.js';
import { buildScenarios } from './pkg/scenarioBuilder.js';
import { createExecFunction } from './pkg/generateExecFunction.js';

// Load config
const { config, payloads } = loadConfig();

// Create scenarios
const scenarios = buildScenarios(config.function.name, payloads);

// Generate K6-script as string
const execFunctionCode = createExecFunction(config.function.name, config.function.serviceFn);

// Script-Body
const script = `import grpc from 'k6/net/grpc';
import { check, sleep } from 'k6';
import exec from 'k6/execution';

// K6 Options
export const options = {
    scenarios: ${JSON.stringify(scenarios, null, 2)}
};

// K6 GRPC client
const client = new grpc.Client();
client.load(['../config'], 'service.proto');

const payloads = ${JSON.stringify(payloads, null, 2)};

${execFunctionCode}

// K6 main function
export default function () {
    ${config.function.name}_exec();
}
`;

// Write the script to a file
const outDir = './generated';
if (!fs.existsSync(outDir)) {
    fs.mkdirSync(outDir);
}
fs.writeFileSync('./generated/k6-script.js', script);
