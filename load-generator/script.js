import grpc from 'k6/net/grpc';
import { randomSeed } from 'k6';
import { Trend } from 'k6/metrics';
import { bfsConfig, bfsSetup, bfsFunction } from './functions/bfs.js';
import { echoConfig, echoSetup, echoFunction } from './functions/echo.js';
import { thumbnailerConfig, thumbnailerSetup, thumbnailerFunction } from './functions/thumbnailer.js';
import { getRandomInt, parseK6Duration } from './utils.js';

// Create global config
const config = {
  // Global Configuration
  workloadSeed: parseInt(__ENV.WORKLOAD_SEED) || Date.now(),
  totalTestDuration: __ENV.TOTAL_TEST_DURATION || "60s",
  minPreallocatedVus: parseInt(__ENV.MIN_PREALLOCATED_VUS) || 10,
  maxPreallocatedVus: parseInt(__ENV.MAX_PREALLOCATED_VUS) || 50,
  minMaxVus: parseInt(__ENV.MIN_MAX_VUS) || 20,
  maxMaxVus: parseInt(__ENV.MAX_MAX_VUS) || 100,
  rampingStartRateMin: parseInt(__ENV.RAMPING_START_RATE_MIN) || 1,
  rampingStartRateMax: parseInt(__ENV.RAMPING_START_RATE_MAX) || 5,
}

// Load executor functions for each function
// Unfortunately, ESM prohibits dynamic exports, so we have to export every function explicitly
export { bfsFunction };
export { echoFunction };
export { thumbnailerFunction };

const functionsToProcess = [
  {
    name: 'bfs',
    setup: bfsSetup,
    execFunction: bfsFunction,
    config: bfsConfig,
    exec: 'bfsFunction',
    configPrefix: 'BFS',
    imageTag: bfsConfig.BFS_IMAGE_TAG
  },
  {
    name: 'echo',
    setup: echoSetup,
    execFunction: echoFunction,
    config: echoConfig,
    exec: 'echoFunction',
    configPrefix: 'ECHO',
    imageTag: echoConfig.ECHO_IMAGE_TAG
  },
  {
    name: 'thumbnailer',
    setup: thumbnailerSetup,
    execFunction: thumbnailerFunction,
    config: thumbnailerConfig,
    exec: 'thumbnailerFunction',
    configPrefix: 'THUMBNAILER',
    imageTag: thumbnailerConfig.THUMBNAILER_IMAGE_TAG
  }
];

// Initialize randomizer with workloadSeed for reproducibility
randomSeed(config.workloadSeed);

// Add function-specific configs to global config
for (const func of functionsToProcess) {
  Object.assign(config, func.config);
}

// Setup function to create the functions before the test
const client = new grpc.Client();
client.load(['./config'], 'common.proto', 'leaf.proto');

export function setup() {
  client.connect('localhost:50050', {
    plaintext: true
  });

  const setupResults = {
    client: client,
  };

  for (const func of functionsToProcess) {
    // Load setup from function file
    if (typeof func.setup === 'function') {
      const result = func.setup(client);
      setupResults[func.name] = result;
    }
  }

  return setupResults;
}

// Generate dynamic scenarios based on the functionsToProcess
const totalTestDurationSeconds = parseK6Duration(config.totalTestDuration);
let generatedScenarios = {};

for (const funcInfo of functionsToProcess) {
  const prefix = funcInfo.configPrefix;
  const minScenarios = config[`${prefix}_MIN_SCENARIOS`];
  const maxScenarios = config[`${prefix}_MAX_SCENARIOS`];

  // Skip if function is disabled or no duration
  if (maxScenarios === 0 || totalTestDurationSeconds === 0) {
    console.log(`Skipping ${funcInfo.name} scenarios (disabled or no duration)`);
    continue;
  }

  const numScenarios = getRandomInt(minScenarios, maxScenarios);
  const avgScenarioDurationSec = totalTestDurationSeconds / numScenarios;
  let currentFunctionStartTimeSec = 0;

  for (let i = 0; i < numScenarios; i++) {
    const scenarioName = `${funcInfo.name}_${i}`;
    const preAllocatedVUs = getRandomInt(config.minPreallocatedVus, config.maxPreallocatedVus);
    let maxVUs = getRandomInt(config.minMaxVus, config.maxMaxVus);
    if (maxVUs < preAllocatedVUs) maxVUs = preAllocatedVUs;

    const isConstantRate = Math.random() < config[`${prefix}_CONSTANT_SCENARIOS_RATIO`];
    const executorType = isConstantRate ? 'constant-arrival-rate' : 'ramping-arrival-rate';

    let scenarioDetails = {
      exec: funcInfo.exec,
      startTime: `${Math.floor(currentFunctionStartTimeSec)}s`,
      preAllocatedVUs: preAllocatedVUs,
      maxVUs: maxVUs,
      tags: {
        scenario_group: funcInfo.name,
        type: executorType,
        image_tag: funcInfo.imageTag
      },
      timeUnit: '1s',
    };

    if (isConstantRate) {
      scenarioDetails = {
        ...scenarioDetails,
        executor: 'constant-arrival-rate',
        duration: `${Math.ceil(avgScenarioDurationSec)}s`,
        rate: getRandomInt(
          config[`${prefix}_CONSTANT_RATE_MIN`],
          config[`${prefix}_CONSTANT_RATE_MAX`]
        )
      };
    } else {
      scenarioDetails = {
        ...scenarioDetails,
        executor: 'ramping-arrival-rate',
        startRate: getRandomInt(config.rampingStartRateMin, config.rampingStartRateMax),
        stages: [{
          target: getRandomInt(
            config[`${prefix}_BURST_TARGET_RATE_MIN`],
            config[`${prefix}_BURST_TARGET_RATE_MAX`]
          ),
          duration: `${Math.ceil(avgScenarioDurationSec)}s`
        }]
      };
    }

    generatedScenarios[scenarioName] = scenarioDetails;
    currentFunctionStartTimeSec += avgScenarioDurationSec;
  }
}

config.persistGeneration = __ENV.PERSIST_GENERATION === 'true' || false;


// Store the persistence data in a variable accessible to handleSummary
const persistenceData = {
  metadata: {
    seed: config.workloadSeed,
    totalDuration: config.totalTestDuration,
    generatedAt: new Date().toISOString(),
    configuration: config,
    /* bfsFunctionId: "bfsFunctionId",
    echoFunctionId: echoFunctionId,
    thumbnailerFunctionId: thumbnailerFunctionId */
  },
  scenarios: generatedScenarios
};

// Export K6 options
export const options = {
  scenarios: generatedScenarios,
  systemTags: ['error', 'group', 'proto', 'scenario', 'service', 'subproto', 'extra_tags', 'metadata', 'vu', 'iter']
};

// Summary function
export function handleSummary(data) {
  if (config.persistGeneration) {
    return {
      'stdout': JSON.stringify(persistenceData, null, 2)
    };
  }
  return {};
}