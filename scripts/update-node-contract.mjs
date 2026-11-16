import fs from 'node:fs';
const path=new URL('../contracts/openapi.json',import.meta.url),doc=JSON.parse(fs.readFileSync(path,'utf8'));
const ref=n=>({$ref:`#/components/schemas/${n}`}),str={type:'string'},num={type:'integer'},bool={type:'boolean'},obj={type:'object',additionalProperties:true};
const schema=(properties,required=Object.keys(properties))=>({type:'object',properties,required,additionalProperties:false});
Object.assign(doc.components.schemas,{
 NodeConnection:schema({address:str,port:num,publish_user:str,stats_user:str,publish_key:str,stats_key:str,version:str,host_key:str},['address','port','publish_user','stats_user','publish_key','stats_key','version']),
 NodeInbound:schema({tag:str,protocol:str,port:num,upload_bytes:str,download_bytes:str,started_at:str,active:bool}),
 NodeLocalUser:schema({name:str,upload_bytes:str,download_bytes:str,started_at:str,active:bool}),
 TrafficPeriod:schema({node_id:str,history_started_at:{anyOf:[str,{type:'null'}]},status:{type:'string',enum:['cumulative','complete','partial','unavailable']}}),
 TrafficUser:schema({user_id:str,display_name:str,node_id:str,node_name:str,upload_bytes:str,download_bytes:str,quality:ref('Quality')}),
 TrafficDay:schema({date:str,upload_bytes:{anyOf:[str,{type:'null'}]},download_bytes:{anyOf:[str,{type:'null'}]},status:{type:'string',enum:['complete','partial','unavailable']}}),
 TrafficReport:schema({scope:{type:'string',enum:['inbounds','local','managed']},trend:{type:'array',items:ref('TrafficDay')},trend_from:str,trend_to:str,nodes:{type:'array',items:ref('Node')},users:{type:'array',items:ref('TrafficUser')},periods:{type:'array',items:ref('TrafficPeriod')},from:str,to:str,timezone:str,as_of:str}),
 NodeDraft:schema({id:str,name:str,region:str,location:str,group_id:str,group_name:str,connection:ref('NodeConnection'),container:str,config_dir:str,public_host:str,server_name:str},['id','name','region','location','group_id','group_name','connection','container','config_dir','public_host']),
 NodeJobInput:schema({kind:{type:'string',enum:['inspect','configure','check']},draft:ref('NodeDraft'),inspection_id:str,expected_version:num,desired_container:str,desired_image:str},['kind','draft','inspection_id','expected_version']),
 NodeJobAccepted:schema({job_id:str}),
 NodeJob:schema({id:str,kind:str,node_id:str,state:{type:'string',enum:['queued','running','completed','failed']},result:{anyOf:[obj,{type:'null'}]},error_code:{anyOf:[str,{type:'null'}]},message:{anyOf:[str,{type:'null'}]},created_at:str,updated_at:str}),
 NodeConfiguration:schema({draft:ref('NodeDraft'),version:num,binding:obj,checked_at:{anyOf:[str,{type:'null'}]},last_check:{anyOf:[obj,{type:'null'}]}}),
 NodeMetadata:schema({draft:ref('NodeDraft'),version:num}),
 NodeSaved:schema({saved:bool,version:num}),
 NodeDiscovery:schema({available:bool,sampled_at:{anyOf:[str,{type:'null'}]},hosts:{type:'array',items:schema({name:str,address:str,online:bool,os:str,node_id:str})},message:str}),
 NodeOnboarding:schema({keys:schema({publish:str,stats:str}),instructions:{type:'array',items:str},release:str}),
});
Object.assign(doc.components.schemas.Node.properties, {
 inbounds:{type:'array',items:ref('NodeInbound')},
 inbound_upload_bytes:{type:'string',pattern:'^(0|[1-9][0-9]*)$'},
 inbound_download_bytes:{type:'string',pattern:'^(0|[1-9][0-9]*)$'},
 local_users:{type:'array',items:ref('NodeLocalUser')},total_inbounds:num,
});
doc.components.schemas.Node.required=[...new Set([...doc.components.schemas.Node.required,'inbounds','inbound_upload_bytes','inbound_download_bytes','local_users','total_inbounds'])];
function endpoint(path,method,id,response,request,status=200){const op={operationId:id,responses:{[status]:{description:'Successful response',content:{'application/json':{schema:ref(response)}}},'401':{description:'Authentication required'},'403':{description:'Origin or CSRF rejected'},'409':{description:'State conflict'},'422':{description:'Invalid request'}}};if(path.includes('{id}'))op.parameters=[{name:'id',in:'path',required:true,schema:str}];if(method==='post'&&!path.endsWith('/retry'))(op.parameters??=[]).push({name:'Idempotency-Key',in:'header',required:true,schema:{type:'string',minLength:16,maxLength:128}});if(request)op.requestBody={required:true,content:{'application/json':{schema:ref(request)}}};(doc.paths[path]??={})[method]=op;}
endpoint('/api/v1/node-discovery','get','get__node_discovery','NodeDiscovery');
endpoint('/api/v1/traffic','get','get__traffic','TrafficReport');
doc.paths['/api/v1/traffic'].get.parameters=['node','protocol','activity','q','user','local_user','scope','from','to'].map(name=>({name,in:'query',schema:{type:'string'},description:['from','to'].includes(name)?'UTC+8 日期 YYYY-MM-DD，首尾包含；两者同时提供，最多 366 天':'统计筛选'}));
endpoint('/api/v1/node-onboarding','get','get__node_onboarding','NodeOnboarding');
endpoint('/api/v1/node-jobs','post','post__node_jobs','NodeJobAccepted','NodeJobInput',202);
endpoint('/api/v1/node-jobs/{id}','get','get__node_job','NodeJob');
endpoint('/api/v1/node-jobs/{id}/retry','post','post__node_job_retry','NodeJobAccepted',null,202);
endpoint('/api/v1/nodes/{id}/config','get','get__node_config','NodeConfiguration');
endpoint('/api/v1/nodes/{id}/metadata','post','post__node_metadata','NodeSaved','NodeMetadata');
fs.writeFileSync(path,JSON.stringify(doc,null,2)+'\n');
