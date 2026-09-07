const http = require('node:http');
const fs = require('node:fs');
const path = require('node:path');

const publicDir = path.join(__dirname, 'public');
const types = { '.css': 'text/css', '.js': 'application/javascript', '.html': 'text/html' };

http.createServer((req, res) => {
  const requested = req.url === '/' ? '/index.html' : req.url;
  const filename = path.resolve(publicDir, `.${requested}`);
  if (!filename.startsWith(publicDir)) return res.writeHead(400).end('Bad request');
  fs.readFile(filename, (error, content) => {
    if (error) return res.writeHead(404).end('Not found');
    res.writeHead(200, { 'content-type': types[path.extname(filename)] || 'text/plain' });
    res.end(content);
  });
}).listen(process.env.PORT || 3000, () => console.log('Agent Factory Demo at http://127.0.0.1:3000'));
