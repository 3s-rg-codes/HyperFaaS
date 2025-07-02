import grpc from 'k6/net/grpc';
import { check } from 'k6';
const client = new grpc.Client();
client.load(['./config'], 'common.proto', 'leaf.proto');

export function setup() {
  const address = __ENV.ADDRESS || 'localhost:50050';
  
  client.connect(address, {
    plaintext: true
  });

  const setupResults = {
    address: address,
    functionIds: {}
  };

  // Create BFS function
  const bfsCreateResponse = client.invoke('leaf.Leaf/CreateFunction', {
    image_tag: { tag: "hyperfaas-bfs-json:latest" },
    config: {
      memory: 256 * 1024 * 1024, // 256MB
      cpu: {
        period: 100000,
        quota: 50000
      }
    }
  });
  
  check(bfsCreateResponse, {
    'No error when creating BFS function': (r) => {
      if (r && r.error) {
        console.log('Error creating BFS function:', r.error);
        return false;
      }
      return true;
    },
    'BFS function created successfully': (r) => r && r.message && r.message.functionID && r.message.functionID.id
  });
  const bfsFunctionId = bfsCreateResponse.message.functionID.id;
  setupResults.functionIds.bfs = bfsFunctionId;
  console.log("bfsFunctionId=", bfsFunctionId);

  // Create Echo function
  const echoCreateResponse = client.invoke('leaf.Leaf/CreateFunction', {
    image_tag: { tag: "hyperfaas-echo-json:latest" },
    config: {
      memory: 256 * 1024 * 1024, // 256MB
      cpu: {
        period: 100000,
        quota: 50000
      }
    }
  });
  
  check(echoCreateResponse, {
    'No error when creating Echo function': (r) => {
      if (r && r.error) {
        console.log('Error creating Echo function:', r.error);
        return false;
      }
      return true;
    },
    'Echo function created successfully': (r) => r && r.message && r.message.functionID && r.message.functionID.id
  });
  const echoFunctionId = echoCreateResponse.message.functionID.id;
  setupResults.functionIds.echo = echoFunctionId;
  console.log("echoFunctionId=", echoFunctionId);

  // Create Thumbnailer function
  const thumbnailerCreateResponse = client.invoke('leaf.Leaf/CreateFunction', {
    image_tag: { tag: "hyperfaas-thumbnailer-json:latest" },
    config: {
      memory: 512 * 1024 * 1024, // 512MB
      cpu: {
        period: 100000,
        quota: 50000
      }
    }
  });
  
  check(thumbnailerCreateResponse, {
    'No error when creating Thumbnailer function': (r) => {
      if (r && r.error) {
        console.log('Error creating Thumbnailer function:', r.error);
        return false;
      }
      return true;
    },
    'Thumbnailer function created successfully': (r) => r && r.message && r.message.functionID && r.message.functionID.id
  });
  const thumbnailerFunctionId = thumbnailerCreateResponse.message.functionID.id;
  setupResults.functionIds.thumbnailer = thumbnailerFunctionId;
  console.log("thumbnailerFunctionId=", thumbnailerFunctionId);

  client.close();
  return setupResults;
}

export const options = {
  scenarios: {
    register_only: {
      executor: 'shared-iterations',
      vus: 1,
      iterations: 1,
    },
  },
};

export function handleSummary(data) {
  const setupData = data.setup_data;
  if (setupData) {
    return {
      'stdout': JSON.stringify(setupData, null, 2)
    };
  }
  return {};
}

// Dummy default function (required by k6)
export default function() {
  // This won't be called since we only use setup
} 