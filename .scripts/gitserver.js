// A small git server for the live suites, run by .scripts/testenv.sh from the Dockhand image itself, which already carries git and node. Every
// repository under /srv/git can be cloned over HTTP: at /open/<name>.git by anyone, and at /private/<name>.git with the login given in GIT_USER and
// GIT_PASSWORD. It only gives; a suite adds a commit inside the container, where nothing but the suites can reach.
const http = require('node:http');
const zlib = require('node:zlib');
const fs = require('node:fs');
const { spawn } = require('node:child_process');

const root = '/srv/git';
const login = 'Basic ' + Buffer.from(`${process.env.GIT_USER}:${process.env.GIT_PASSWORD}`).toString('base64');
const asked = /^\/(open|private)\/([a-z0-9][a-z0-9-]*\.git)\/(info\/refs|git-upload-pack)$/;

// say ends an answer that carries no repository, with one line saying why
function say(res, status, why, headers = {}) {
  res.writeHead(status, { 'Content-Type': 'text/plain', ...headers });
  res.end(why + '\n');
}

http.createServer((req, res) => {
  const url = new URL(req.url, 'http://git');
  const found = asked.exec(url.pathname);
  if (!found || !fs.existsSync(`${root}/${found[2]}`)) return say(res, 404, 'no such repository');

  if (found[1] === 'private' && req.headers.authorization !== login) return say(res, 401, 'this repository asks for a login', { 'WWW-Authenticate': 'Basic realm="git"' });

  const repository = `${root}/${found[2]}`;
  const version = req.headers['git-protocol'] || '';
  const env = { ...process.env, GIT_PROTOCOL: version };

  // the first call of a clone or fetch: what the repository holds
  if (found[3] === 'info/refs') {
    if (url.searchParams.get('service') !== 'git-upload-pack') return say(res, 403, 'this server only gives');

    res.writeHead(200, { 'Content-Type': 'application/x-git-upload-pack-advertisement', 'Cache-Control': 'no-cache' });
    // the older ways of talking are told which service is answering before anything else; the newest is not
    if (!version.includes('version=2')) res.write('001e# service=git-upload-pack\n0000');
    spawn('git', ['upload-pack', '--stateless-rpc', '--advertise-refs', repository], { env }).stdout.pipe(res);
    return;
  }

  // every call after it: git says what it wants and is sent it, having squeezed what it says when that is long
  if (req.method !== 'POST') return say(res, 405, 'post what is wanted');

  res.writeHead(200, { 'Content-Type': 'application/x-git-upload-pack-result', 'Cache-Control': 'no-cache' });
  const git = spawn('git', ['upload-pack', '--stateless-rpc', repository], { env });
  (req.headers['content-encoding'] === 'gzip' ? req.pipe(zlib.createGunzip()) : req).pipe(git.stdin);
  git.stdout.pipe(res);
}).listen(8080, () => console.log('git server: giving what is under ' + root + ' on :8080'));
