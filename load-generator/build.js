import fs from 'fs';

import { loadConfig } from './pkg/loadConfig.js';
import { buildScenarios } from './pkg/buildScenarios.js';

// Load config
const { config, payloads } = loadConfig();

// Create k6 scenarios
const scenarios = buildScenarios(config.function.name, payloads);

// Generate the k6 script by reading the template and replacing the placeholders with correct values
const template = fs.readFileSync('./template/k6-script-template.js', 'utf8');

const script = template
    .replace('__SCENARIOS__', JSON.stringify(scenarios, null, 2))
    .replace('__PROTO_FILE__', config.metaData.protoFile)
    .replace('__PAYLOADS__', JSON.stringify(payloads, null, 2))
    .replace('__FUNCTION_NAME__', config.function.name)
    .replace('__SERVICE_FN__', config.function.serviceFn);

// Write the script to a file
const outDir = './generated';
if (!fs.existsSync(outDir)) {
    fs.mkdirSync(outDir);
}
fs.writeFileSync('./generated/k6-script.js', script);
