import { ready } from '../runtime/index.js';

let isCold = true;

ready((req, res) => {
  const chunks = [];
  req.on('data', (chunk) => chunks.push(chunk));
  req.on('end', () => {
    res.setHeader('X-Cold', isCold ? '1' : '0');
    isCold = false;
    res.writeHead(200);
    res.end(Buffer.concat(chunks));
  });
});
