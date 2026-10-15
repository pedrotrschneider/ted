import assert from 'node:assert/strict';

// Explicitly restricted to the disposable integration stack's published port.
export async function checkLocalPermissions(){
 const base='http://127.0.0.1:18100';
 const call=async(path,method='GET',cookie,body,origin)=>fetch(base+path,{method,headers:{Connection:'close','Content-Type':'application/json',...(cookie?{Cookie:cookie}:{}),...(origin?{Origin:origin}:{})},body:body===undefined?undefined:JSON.stringify(body),signal:AbortSignal.timeout(10000)});
 const login=await call('/api/auth/login','POST',undefined,{username:'stream-test',password:''});assert.equal(login.status,200);const admin=login.headers.get('set-cookie').split(';')[0];await login.arrayBuffer();
 const created=await call('/api/admin/users','POST',admin,{username:'download-viewer',display_name:'Download viewer',password:'',role:'user'});assert.equal(created.status,201);await created.arrayBuffer();
 const userLogin=await call('/api/auth/login','POST',undefined,{username:'download-viewer',password:''});assert.equal(userLogin.status,200);const user=userLogin.headers.get('set-cookie').split(';')[0];await userLogin.arrayBuffer();
 const status=await call('/api/movies/tt1345836/local','GET',user);assert.equal(status.status,200);assert.equal((await status.json()).eligible,true);
 const requested=await call('/api/movies/tt1345836/local','POST',user);assert.equal(requested.status,202);await requested.arrayBuffer();
 for(const [path,method,cookie,origin,expected] of [
  ['/api/admin/movies/tt1345836/local','DELETE',user,undefined,403],
  ['/api/admin/movies/tt1345836/local/cancel','POST',user,undefined,403],
  ['/api/movies/tt1345836/local','POST',undefined,undefined,401],
  ['/api/movies/tt1345836/local','POST',user,'https://untrusted.example',403],
 ]){const response=await call(path,method,cookie,undefined,origin);assert.equal(response.status,expected);await response.arrayBuffer()}
 return {userCanRequest:true,userCannotRemoveOrCancel:true,anonymousDenied:true,crossOriginDenied:true};
}
