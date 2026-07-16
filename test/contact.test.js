import test from 'node:test';
import assert from 'node:assert/strict';
import { once } from 'node:events';
import { createApp } from '../server.js';

async function start(overrides = {}) {
  const sent = [];
  const app = createApp({
    publicDir: new URL('../', import.meta.url),
    config: {
      mailgunApiKey: 'test-key', mailgunDomain: 'mg.example.com',
      contactTo: 'hello@example.com', contactFrom: 'ODF <contact@mg.example.com>',
    },
    sendMail: overrides.sendMail ?? (async message => sent.push(message)),
  });
  app.listen(0, '127.0.0.1'); await once(app, 'listening');
  return { app, sent, base: `http://127.0.0.1:${app.address().port}` };
}
async function stop(app) { app.close(); await once(app, 'close'); }
async function submit(base, body) {
  return fetch(`${base}/api/contact`, { method: 'POST', headers: {'content-type':'application/json'}, body: JSON.stringify(body) });
}

test('valid contact submission sends mail', async () => {
  const { app, sent, base } = await start();
  try {
    const response = await submit(base, { name:'  Ada Lovelace ', email:'ADA@example.com', company:' Analytical Engines ', message:'  I have a difficult system to build. ', website:'' });
    assert.equal(response.status, 202); assert.deepEqual(await response.json(), {ok:true});
    assert.equal(sent.length, 1); assert.equal(sent[0].replyTo, 'ADA@example.com');
    assert.match(sent[0].subject, /Ada Lovelace/); assert.match(sent[0].text, /Analytical Engines/);
  } finally { await stop(app); }
});

test('invalid submission is rejected without sending', async () => {
  const { app, sent, base } = await start();
  try {
    const response = await submit(base, {name:'',email:'bad',message:'short',website:''});
    assert.equal(response.status, 400); const body=await response.json();
    assert.equal(body.ok,false); assert.ok(body.errors.name); assert.ok(body.errors.email); assert.ok(body.errors.message); assert.equal(sent.length,0);
  } finally { await stop(app); }
});

test('honeypot receives neutral success without sending', async () => {
  const { app, sent, base } = await start();
  try {
    const response = await submit(base, {name:'Bot',email:'bot@example.com',message:'Long enough spam message.',website:'spam.example'});
    assert.equal(response.status,202); assert.equal(sent.length,0);
  } finally { await stop(app); }
});

test('mail provider failure is sanitized', async () => {
  const { app, base } = await start({sendMail:async()=>{throw new Error('provider secret')}});
  try {
    const response=await submit(base,{name:'Grace',email:'grace@example.com',message:'This is a sufficiently long message.',website:''});
    assert.equal(response.status,502); assert.deepEqual(await response.json(),{ok:false,error:'Message delivery failed. Please try again.'});
  } finally { await stop(app); }
});

test('serves health and static site', async () => {
  const { app, base } = await start();
  try {
    const health=await fetch(`${base}/healthz`); assert.equal(await health.text(),'ok\n');
    const home=await fetch(base); assert.equal(home.status,200); assert.match(await home.text(),/Oregon Dev Foundry/);
  } finally { await stop(app); }
});
