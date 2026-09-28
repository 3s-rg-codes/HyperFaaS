import http from 'node:http';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import grpc from '@grpc/grpc-js';
import protoLoader from '@grpc/proto-loader';

const DEFAULT_FUNCTION_PORT = '50052';

function requiredEnv(name) {
  const value = process.env[name];
  if (!value) {
    throw new Error(`hyperfaas runtime: ${name} is required`);
  }
  return value;
}

async function signalReady(controllerAddress, instanceId) {
  const runtimeDir = path.dirname(fileURLToPath(import.meta.url));
  const protoPath = path.resolve(runtimeDir, 'ready.proto');
  const packageDefinition = protoLoader.loadSync(protoPath, {
    keepCase: false,
    longs: String,
    enums: String,
    defaults: true,
    oneofs: true
  });
  const loaded = grpc.loadPackageDefinition(packageDefinition).hyperfaas;
  const client = new loaded.SandboxService(controllerAddress, grpc.credentials.createInsecure());

  await new Promise((resolve, reject) => {
    client.SignalReady({ instanceId }, (err) => {
      if (err) reject(err);
      else resolve();
    });
  });
}

export function ready(handler) {
  if (typeof handler !== 'function') {
    throw new Error('hyperfaas runtime: handler must be a function');
  }

  const controllerAddress = requiredEnv('CONTROLLER_ADDRESS');
  const instanceId = requiredEnv('INSTANCE_ID');
  const functionPort = process.env.FUNCTION_PORT || DEFAULT_FUNCTION_PORT;

  const server = http.createServer(handler);
  server.listen(Number(functionPort), '0.0.0.0', async () => {
    try {
      await signalReady(controllerAddress, instanceId);
      console.log(`hyperfaas node runtime ready on ${functionPort}`);
    } catch (err) {
      console.error('hyperfaas node runtime failed to signal ready', err);
      process.exit(1);
    }
  });
}
