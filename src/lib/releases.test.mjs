import test from 'node:test';
import assert from 'node:assert/strict';
import { parseRelease, detectOS } from './releases.mjs';
const asset = (name, url = 'https://github.com/Sipaha/spk-ocular/releases/download/v0.1.0/' + name) => ({name, browser_download_url:url, size:1024});
const release = assets => ({tag_name:'v0.1.0',assets});
test('all six platforms include desktop, browser and matching checksum assets', () => {
 const files=[];
 for (const os of ['linux','windows','darwin']) for (const arch of ['amd64','arm64']) for (const prefix of ['spk-ocular','spk-ocular-browser']) {
  const name=`${prefix}_0.1.0_${os}_${arch}.${os==='windows'?'zip':'tar.gz'}`;
  files.push(asset(name), asset(name+'.sha256'));
 }
 const result=parseRelease(release(files));
 assert.equal(result.files.length,12);
 assert.equal(result.files.filter(f=>f.browser).length,6);
 assert.ok(result.files.every(f=>f.checksum));
});
test('rejects invented, stale, cross-platform, malicious and duplicate assets', () => {
 const good=asset('spk-ocular_0.1.0_windows_arm64.msi');
 const result=parseRelease(release([good,good,asset('spk-ocular_0.2.0_linux_amd64.deb'),asset('spk-ocular_0.1.0_linux_amd64.msi'),asset('spk-ocular-browser_0.1.0_linux_amd64.deb'),asset('spk-ocular_0.1.0_linux_amd64.deb','https://evil.example/payload'),asset('other.exe')]));
 assert.deepEqual(result.files.map(f=>f.name),[good.name]);
 assert.equal(result.files[0].checksum,undefined);
});
test('missing and prerelease data cannot masquerade as a stable download', () => {
 for (const data of [null,{},[],{...release([]),draft:true},{...release([]),prerelease:true},{...release([]),tag_name:'0.1.0-rc.1'}]) assert.equal(parseRelease(data),null);
 assert.deepEqual(parseRelease(release([])).files,[]);
});
test('detects desktop OS without guessing architecture or treating an iPad as a Mac', () => {
 assert.equal(detectOS('Windows NT 10.0; Win64; x64'),'windows');
 assert.equal(detectOS('Macintosh; Intel Mac OS X'),'darwin');
 assert.equal(detectOS('Macintosh; Intel Mac OS X',5),'');
 assert.equal(detectOS('Linux x86_64'),'linux');
 assert.equal(detectOS('Linux Android Mobile'),'');
 assert.equal(detectOS('X11; CrOS x86_64'),'');
});

test('desktop installers precede archives and browser builds regardless of upload order', () => {
 const names=['spk-ocular-browser_0.1.0_linux_amd64.tar.gz','spk-ocular_0.1.0_linux_amd64.tar.gz','spk-ocular_0.1.0_linux_amd64.rpm','spk-ocular_0.1.0_linux_amd64.deb'];
 for(const input of [names,[...names].reverse()]) {
  assert.deepEqual(parseRelease(release(input.map(name=>asset(name)))).files.map(file=>file.name),[names[3],names[2],names[1],names[0]]);
 }
});
