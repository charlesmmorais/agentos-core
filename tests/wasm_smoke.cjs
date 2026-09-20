// Executes our trusted build only. Node WASI is not an untrusted-code sandbox.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const mode = process.argv[2], modulePath = process.argv[3];
(async () => {
  if (mode === 'wasi') {
    const { WASI } = require('node:wasi');
    const wasi = new WASI({ version: 'preview1', args: ['agentos-sim'], env: {}, preopens: {}, returnOnExit: true });
    const instance = await WebAssembly.instantiate(await WebAssembly.compile(fs.readFileSync(modulePath)), {wasi_snapshot_preview1: wasi.wasiImport});
    process.exitCode = wasi.start(instance);
  } else if (mode === 'js') {
    globalThis.crypto ||= require('node:crypto').webcrypto;
    require(path.resolve(process.argv[4]));
    const go = new Go();
    const { instance } = await WebAssembly.instantiate(fs.readFileSync(modulePath), go.importObject);
    go.run(instance).catch(err => { console.error(err); process.exit(1); });
    assert.equal(typeof globalThis.agentosSimulate, 'function');
    const protocol = {mission:'test', workspace:'/data', script:'/script.py', interval_seconds:1, timeout_seconds:1, max_cycles:2};
    const result = JSON.parse(agentosSimulate(JSON.stringify(protocol)));
    assert.equal(result.valid,true); assert.equal(result.execution,false); assert.equal(result.persistence,false);
    assert.ok(JSON.parse(agentosSimulate('{')).error);
    assert.ok(JSON.parse(agentosSimulate(JSON.stringify({...protocol, unexpected:true}))).error);
    assert.ok(JSON.parse(agentosSimulate(JSON.stringify({...protocol,max_cycles:0}))).error);
    console.log('Go JS/WASM validation passed (Node host; browser rendering not tested)');
    process.exit(0);
  } else throw new Error('expected wasi or js');
})().catch(err => {console.error(err); process.exit(1);});
