import grpc from 'k6/net/grpc';
import { randomSeed } from 'k6';
import { bfsFunction } from './functions/bfs-sequential.js';
import { echoFunction } from './functions/echo-sequential.js';
import { thumbnailerFunction } from './functions/thumbnailer-sequential.js';
import { getRandomInt, parseK6Duration } from './utils.js';
import { SharedArray } from 'k6/data';
import encoding from 'k6/encoding';
import http from 'k6/http';
// Load executor functions for each function
export { bfsFunction };
export { echoFunction };
export { thumbnailerFunction };

// a SharedArray makes it so that all the code inside that function is executed only once, and the data is shared between all the VUs. not copied for each VU.
const data = new SharedArray('data', function() {
    const config = {
    workloadSeed: parseInt(__ENV.WORKLOAD_SEED) || Date.now(),
    totalTestDuration: __ENV.TOTAL_TEST_DURATION || "60s",
    minPreallocatedVus: parseInt(__ENV.MIN_PREALLOCATED_VUS) || 10,
    maxPreallocatedVus: parseInt(__ENV.MAX_PREALLOCATED_VUS) || 50,
    minMaxVus: parseInt(__ENV.MIN_MAX_VUS) || 20,
    maxMaxVus: parseInt(__ENV.MAX_MAX_VUS) || 100,
    rampingStartRateMin: parseInt(__ENV.RAMPING_START_RATE_MIN) || 1,
    rampingStartRateMax: parseInt(__ENV.RAMPING_START_RATE_MAX) || 5,
    functionTimeoutSeconds: __ENV.FUNCTION_TIMEOUT_SECONDS + "s",
    address: __ENV.ADDRESS,
    runId: __ENV.RUN_ID,

    BFS_MIN_SCENARIOS: parseInt(__ENV.BFS_MIN_SCENARIOS) || 1,
    BFS_MAX_SCENARIOS: parseInt(__ENV.BFS_MAX_SCENARIOS) || 3,
    BFS_CONSTANT_SCENARIOS_RATIO: parseFloat(__ENV.BFS_CONSTANT_SCENARIOS_RATIO) || 0.5,
    BFS_CONSTANT_RATE_MIN: parseInt(__ENV.BFS_CONSTANT_RATE_MIN) || 5,
    BFS_CONSTANT_RATE_MAX: parseInt(__ENV.BFS_CONSTANT_RATE_MAX) || 20,
    BFS_BURST_TARGET_RATE_MIN: parseInt(__ENV.BFS_BURST_TARGET_RATE_MIN) || 10,
    BFS_BURST_TARGET_RATE_MAX: parseInt(__ENV.BFS_BURST_TARGET_RATE_MAX) || 40,
    BFS_IMAGE_TAG: __ENV.BFS_IMAGE_TAG || 'hyperfaas-bfs-json:latest',
    BFS_FUNCTION_ID: __ENV.BFS_FUNCTION_ID,

    ECHO_MIN_SCENARIOS: parseInt(__ENV.ECHO_MIN_SCENARIOS) || 1,
    ECHO_MAX_SCENARIOS: parseInt(__ENV.ECHO_MAX_SCENARIOS) || 3,
    ECHO_CONSTANT_SCENARIOS_RATIO: parseFloat(__ENV.ECHO_CONSTANT_SCENARIOS_RATIO) || 0.5,
    ECHO_CONSTANT_RATE_MIN: parseInt(__ENV.ECHO_CONSTANT_RATE_MIN) || 5,
    ECHO_CONSTANT_RATE_MAX: parseInt(__ENV.ECHO_CONSTANT_RATE_MAX) || 20,
    ECHO_BURST_TARGET_RATE_MIN: parseInt(__ENV.ECHO_BURST_TARGET_RATE_MIN) || 10,
    ECHO_BURST_TARGET_RATE_MAX: parseInt(__ENV.ECHO_BURST_TARGET_RATE_MAX) || 40,
    ECHO_IMAGE_TAG: __ENV.ECHO_IMAGE_TAG || 'hyperfaas-echo:latest',
    ECHO_FUNCTION_ID: __ENV.ECHO_FUNCTION_ID,

    THUMBNAILER_MIN_SCENARIOS: parseInt(__ENV.THUMBNAILER_MIN_SCENARIOS) || 1,
    THUMBNAILER_MAX_SCENARIOS: parseInt(__ENV.THUMBNAILER_MAX_SCENARIOS) || 3,
    THUMBNAILER_CONSTANT_SCENARIOS_RATIO: parseFloat(__ENV.THUMBNAILER_CONSTANT_SCENARIOS_RATIO) || 0.5,
    THUMBNAILER_CONSTANT_RATE_MIN: parseInt(__ENV.THUMBNAILER_CONSTANT_RATE_MIN) || 5,
    THUMBNAILER_CONSTANT_RATE_MAX: parseInt(__ENV.THUMBNAILER_CONSTANT_RATE_MAX) || 20,
    THUMBNAILER_BURST_TARGET_RATE_MIN: parseInt(__ENV.THUMBNAILER_BURST_TARGET_RATE_MIN) || 10,
    THUMBNAILER_BURST_TARGET_RATE_MAX: parseInt(__ENV.THUMBNAILER_BURST_TARGET_RATE_MAX) || 40,
    THUMBNAILER_IMAGE_TAG: __ENV.THUMBNAILER_IMAGE_TAG || 'hyperfaas-thumbnailer-json:latest',
    THUMBNAILER_FUNCTION_ID: __ENV.THUMBNAILER_FUNCTION_ID,
    }

    const functionsToProcess = [
    {
        name: 'bfs',
        exec: 'bfsFunction',
        configPrefix: 'BFS',
        imageTag: config.BFS_IMAGE_TAG,
        functionId: config.BFS_FUNCTION_ID
    },
    {
        name: 'echo',
        exec: 'echoFunction',
        configPrefix: 'ECHO',
        imageTag: config.ECHO_IMAGE_TAG,
        functionId: config.ECHO_FUNCTION_ID
    },
    {
        name: 'thumbnailer',
        exec: 'thumbnailerFunction',
        configPrefix: 'THUMBNAILER',
        imageTag: config.THUMBNAILER_IMAGE_TAG,
        functionId: config.THUMBNAILER_FUNCTION_ID
    }
    ];

    // Initialize randomizer with workloadSeed for reproducibility
    randomSeed(config.workloadSeed);

    // Generate dynamic scenarios based on the functionsToProcess
    const totalTestDurationSeconds = parseK6Duration(config.totalTestDuration);
    console.log(`config.totalTestDuration=${config.totalTestDuration}, totalTestDurationSeconds=${totalTestDurationSeconds}`);
    let generatedScenarios = {};

    for (const funcInfo of functionsToProcess) {
    const prefix = funcInfo.configPrefix;
    const minScenarios = config[`${prefix}_MIN_SCENARIOS`];
    const maxScenarios = config[`${prefix}_MAX_SCENARIOS`];

    // Skip if function is disabled or no duration
    if (maxScenarios === 0 || totalTestDurationSeconds === 0) {
        console.log(`Skipping ${funcInfo.name} scenarios (disabled or no duration)`);
        console.log(`minScenarios=${minScenarios}, maxScenarios=${maxScenarios}, totalTestDurationSeconds=${totalTestDurationSeconds}`);
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
            image_tag: funcInfo.imageTag,
            run_id: config.runId 
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

    // Store the persistence data with run_id
    const persistenceData = {
    metadata: {
        runId: config.runId,
        seed: config.workloadSeed,
        totalDuration: config.totalTestDuration,
        generatedAt: new Date().toISOString(),
        configuration: config,
        functionTimeoutSeconds: config.functionTimeoutSeconds,
        bfsFunctionId: config.BFS_FUNCTION_ID,
        echoFunctionId: config.ECHO_FUNCTION_ID,
        thumbnailerFunctionId: config.THUMBNAILER_FUNCTION_ID
    },
    scenarios: generatedScenarios
    };
    const imageDataB64 = encoding.b64encode(open("pic.jpg"));
    const dataArray = [config, persistenceData, generatedScenarios, imageDataB64];
    return dataArray;

});

// Setup function - no longer registers functions, just returns connection info
export function setup() {
  console.log("bfs", data[1].metadata.bfsFunctionId)
  console.log("echo", data[1].metadata.echoFunctionId)
  console.log("thumbnailer", data[1].metadata.thumbnailerFunctionId)
  const imageDataB64 = encoding.b64encode(http.get("https://picsum.photos/200/300").body);
  return {
    timeout: data[0].functionTimeoutSeconds,
    address: data[0].address,
    bfs: data[1].metadata.bfsFunctionId,
    echo: data[1].metadata.echoFunctionId,
    thumbnailer: data[1].metadata.thumbnailerFunctionId,
    imageDataB64: imageDataB64,
    runId: data[0].runId
  };
}

export const options = {
  scenarios: data[2],
  systemTags: ['error', 'group', 'proto', 'scenario', 'service', 'subproto', 'extra_tags', 'metadata', 'vu', 'iter']
};

// Summary function
export function handleSummary() {
  if (data[0].persistGeneration) {
    return {
      'stdout': JSON.stringify(data[1], null, 2)
    };
  }
  return {};
} 