import http from 'node:http';
import { readFile } from 'node:fs/promises';
import { extname, join, normalize } from 'node:path';
import { fileURLToPath } from 'node:url';

const TYPES={'.html':'text/html; charset=utf-8','.css':'text/css; charset=utf-8','.js':'text/javascript; charset=utf-8','.svg':'image/svg+xml'};
const EMAIL=/^[^\s@]+@[^\s@]+\.[^\s@]+$/;
const clean=(v,max)=>String(v??'').trim().slice(0,max);
const REQUEST_TIMEOUT_MS=10_000;

export function validateContact(input={}) {
  const data={name:clean(input.name,100),email:clean(input.email,254),company:clean(input.company,120),message:clean(input.message,5000),website:clean(input.website,200)};
  const errors={};
  if(data.name.length<2) errors.name='Please enter your name.';
  if(!EMAIL.test(data.email)) errors.email='Please enter a valid email address.';
  if(data.message.length<20) errors.message='Please include at least 20 characters.';
  return {data,errors};
}

async function postForm(url,form,headers,fetchImpl,timeoutMs=REQUEST_TIMEOUT_MS) {
  const response=await fetchImpl(url,{method:'POST',headers,body:form,signal:AbortSignal.timeout(timeoutMs)});
  const body=await response.text();
  return {response,body};
}

export async function sendWithMailgun(message,config,fetchImpl=fetch) {
  const form=new FormData();
  form.set('from',config.contactFrom); form.set('to',config.contactTo);
  form.set('h:Reply-To',message.replyTo); form.set('subject',message.subject); form.set('text',message.text);
  const auth=Buffer.from(`api:${config.mailgunApiKey}`).toString('base64');
  const region=(config.mailgunRegion||'us').toLowerCase();
  const host=region==='eu'?'https://api.eu.mailgun.net':'https://api.mailgun.net';
  const {response}=await postForm(`${host}/v3/${config.mailgunDomain}/messages`,form,{authorization:`Basic ${auth}`},fetchImpl);
  if(!response.ok) throw new Error(`Mailgun returned ${response.status}`);
}

export async function verifyTurnstile(token,config,fetchImpl=fetch) {
  const form=new FormData();
  form.set('secret',config.turnstileSecretKey); form.set('response',token);
  const {response,body}=await postForm('https://challenges.cloudflare.com/turnstile/v0/siteverify',form,{},fetchImpl);
  if(!response.ok) throw new Error(`Turnstile returned ${response.status}`);
  const result=JSON.parse(body);
  return result.success===true&&result.action==='contact';
}

async function readJson(req) {
  const chunks=[]; let size=0;
  for await (const chunk of req) { size+=chunk.length; if(size>16_384) throw Object.assign(new Error('too large'),{status:413}); chunks.push(chunk); }
  try {
    const value=JSON.parse(Buffer.concat(chunks).toString('utf8'));
    if(value===null||typeof value!=='object'||Array.isArray(value)) throw new Error('invalid shape');
    return value;
  } catch { throw Object.assign(new Error('invalid json'),{status:400}); }
}

export function createApp({publicDir=new URL('.',import.meta.url),config={},sendMail,verifyChallenge}={}) {
  const root=fileURLToPath(publicDir); const deliver=sendMail??(message=>sendWithMailgun(message,config));
  const verify=verifyChallenge??(token=>verifyTurnstile(token,config));
  return http.createServer(async(req,res)=>{
    const json=(status,body)=>{res.writeHead(status,{'content-type':'application/json; charset=utf-8','cache-control':'no-store','x-content-type-options':'nosniff'});res.end(JSON.stringify(body));};
    try {
      const url=new URL(req.url,'http://localhost');
      if(req.method==='GET'&&(url.pathname==='/healthz'||url.pathname==='/up')){res.writeHead(200,{'content-type':'text/plain; charset=utf-8','cache-control':'no-store'});return res.end('ok\n');}
      if(req.method==='GET'&&url.pathname==='/api/contact-config') return json(200,{turnstileSiteKey:config.turnstileSiteKey});
      if(req.method==='POST'&&url.pathname==='/api/contact'){
        if(!String(req.headers['content-type']||'').startsWith('application/json')) return json(415,{ok:false,error:'Content-Type must be application/json.'});
        const input=await readJson(req);
        const {data,errors}=validateContact(input);
        if(data.website) return json(202,{ok:true});
        if(Object.keys(errors).length) return json(400,{ok:false,errors});
        const challengeToken=clean(input.challengeToken,2048);
        if(!challengeToken||!await verify(challengeToken)) return json(400,{ok:false,error:'Please complete the verification challenge.'});
        const company=data.company?`\nCompany: ${data.company}`:'';
        await deliver({replyTo:data.email,subject:`Website enquiry from ${data.name}`,text:`Name: ${data.name}\nEmail: ${data.email}${company}\n\nMessage:\n${data.message}`});
        return json(202,{ok:true});
      }
      if(req.method!=='GET'&&req.method!=='HEAD') return json(405,{ok:false,error:'Method not allowed.'});
      const requested=url.pathname==='/'?'index.html':url.pathname.slice(1);
      const safe=normalize(requested).replace(/^(\.\.(\/|\\|$))+/, '');
      if(!['index.html','styles.css','script.js'].includes(safe)){res.writeHead(404);return res.end('Not found\n');}
      const body=await readFile(join(root,safe));
      res.writeHead(200,{'content-type':TYPES[extname(safe)]||'application/octet-stream','x-content-type-options':'nosniff','referrer-policy':'strict-origin-when-cross-origin'});
      if(req.method==='HEAD') return res.end(); res.end(body);
    } catch(error) {
      if(error.status) return json(error.status,{ok:false,error:error.status===413?'Submission is too large.':'Invalid request.'});
      console.error('contact request failed:',error.message); json(502,{ok:false,error:'Message delivery failed. Please try again.'});
    }
  });
}

if(process.argv[1]===fileURLToPath(import.meta.url)){
  const required=['MAILGUN_API_KEY','MAILGUN_DOMAIN','CONTACT_TO','CONTACT_FROM','TURNSTILE_SITE_KEY','TURNSTILE_SECRET_KEY'];
  const missing=required.filter(k=>!process.env[k]);
  if(missing.length){console.error(`Missing required environment variables: ${missing.join(', ')}`);process.exit(1);}
  const port=Number(process.env.PORT||8080);
  const app=createApp({config:{mailgunApiKey:process.env.MAILGUN_API_KEY,mailgunDomain:process.env.MAILGUN_DOMAIN,mailgunRegion:process.env.MAILGUN_REGION||'us',contactTo:process.env.CONTACT_TO,contactFrom:process.env.CONTACT_FROM,turnstileSiteKey:process.env.TURNSTILE_SITE_KEY,turnstileSecretKey:process.env.TURNSTILE_SECRET_KEY}});
  app.listen(port,'0.0.0.0',()=>console.log(`Oregon Dev Foundry listening on ${port}`));
}
