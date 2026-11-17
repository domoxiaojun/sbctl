import fs from 'node:fs';
const path=new URL('../contracts/openapi.json',import.meta.url),doc=JSON.parse(fs.readFileSync(path,'utf8'));
const ref=n=>({$ref:`#/components/schemas/${n}`}),str={type:'string'},num={type:'integer'},bool={type:'boolean'},obj={type:'object',additionalProperties:true};
const schema=(properties,required=Object.keys(properties))=>({type:'object',properties,required,additionalProperties:false});
Object.assign(doc.components.schemas,{
 NodeConnection:schema({address:str,port:num,publish_user:str,stats_user:str,publish_key:str,stats_key:str,version:str,host_key:str},['address','port','publish_user','stats_user','publish_key','stats_key','version']),
 NodeInbound:schema({tag:str,protocol:str,port:num,upload_bytes:str,download_bytes:str,started_at:str,active:bool}),
 NodeLocalUser:schema({name:str,upload_bytes:str,download_bytes:str,started_at:str,active:bool}),
 TrafficPeriod:schema({node_id:str,history_started_at:{anyOf:[str,{type:'null'}]},status:{type:'string',enum:['cumulative','complete','partial','unavailable']}}),
 TrafficDay:schema({date:{type:'string',description:'按日为 YYYY-MM-DD，按小时为 YYYY-MM-DDTHH:00，按月为 YYYY-MM，均为 UTC+8'},upload_bytes:{anyOf:[str,{type:'null'}]},download_bytes:{anyOf:[str,{type:'null'}]},status:{type:'string',enum:['complete','partial','unavailable']}}),
 TrafficReport:schema({scope:{type:'string',enum:['inbounds','local']},granularity:{type:'string',enum:['hour','day','month']},trend:{type:'array',items:ref('TrafficDay')},trend_from:str,trend_to:str,nodes:{type:'array',items:ref('Node')},periods:{type:'array',items:ref('TrafficPeriod')},from:str,to:str,timezone:str,as_of:str}),
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
 config_event_seq:num,config_semantic_hash:str,config_sync_state:{type:'string',enum:['unknown','synced','pending_restart','pending_sync','conflict']},
 config_sync_at:{anyOf:[str,{type:'null'}]},config_sync_error:{anyOf:[str,{type:'null'}]},config_external_drift:bool,
});
doc.components.schemas.Node.required=[...new Set([...doc.components.schemas.Node.required,'inbounds','inbound_upload_bytes','inbound_download_bytes','local_users','total_inbounds','config_event_seq','config_semantic_hash','config_sync_state','config_sync_at','config_sync_error','config_external_drift'])];
function endpoint(path,method,id,response,request,status=200){const op={operationId:id,responses:{[status]:{description:'Successful response',content:{'application/json':{schema:ref(response)}}},'401':{description:'Authentication required'},'403':{description:'Origin or CSRF rejected'},'409':{description:'State conflict'},'422':{description:'Invalid request'}}};if(path.includes('{id}'))op.parameters=[{name:'id',in:'path',required:true,schema:str}];if(method==='post'&&!path.endsWith('/retry'))(op.parameters??=[]).push({name:'Idempotency-Key',in:'header',required:true,schema:{type:'string',minLength:16,maxLength:128}});if(request)op.requestBody={required:true,content:{'application/json':{schema:ref(request)}}};(doc.paths[path]??={})[method]=op;}
endpoint('/api/v1/node-discovery','get','get__node_discovery','NodeDiscovery');
endpoint('/api/v1/traffic','get','get__traffic','TrafficReport');
doc.paths['/api/v1/traffic'].get.parameters=['node','protocol','activity','q','local_user','scope','from','to'].map(name=>({name,in:'query',schema:{type:'string'},description:['from','to'].includes(name)?'UTC+8 日期 YYYY-MM-DD，首尾包含；两者同时提供，最多 366 天':'统计筛选'}));
doc.paths['/api/v1/traffic'].get.parameters.push({name:'granularity',in:'query',schema:{type:'string',enum:['hour','day','month']},description:'趋势粒度，默认按日；按小时时日期区间最多 7 天，未选日期时返回最近两天；按月时未选日期返回最近 12 个月'});
endpoint('/api/v1/node-onboarding','get','get__node_onboarding','NodeOnboarding');
endpoint('/api/v1/node-jobs','post','post__node_jobs','NodeJobAccepted','NodeJobInput',202);
endpoint('/api/v1/node-jobs/{id}','get','get__node_job','NodeJob');
endpoint('/api/v1/node-jobs/{id}/retry','post','post__node_job_retry','NodeJobAccepted',null,202);
endpoint('/api/v1/nodes/{id}/config','get','get__node_config','NodeConfiguration');
endpoint('/api/v1/nodes/{id}/metadata','post','post__node_metadata','NodeSaved','NodeMetadata');
fs.writeFileSync(path,JSON.stringify(doc,null,2)+'\n');
// Management workflows use explicit metadata versions and preserve historical identity.
Object.assign(doc.components.schemas,{
 UserMetadata:schema({version:num,display_name:{type:'string',minLength:1,maxLength:40},note:{type:'string',maxLength:160}}),
 UserVersion:schema({id:str,version:num,revision:num}),
 ArchiveUsers:schema({users:{type:'array',minItems:1,maxItems:50,items:ref('UserVersion')},archived:bool}),
 DeleteUsers:schema({users:{type:'array',minItems:1,maxItems:50,items:ref('UserVersion')}}),
 ArchiveResult:schema({saved:bool,count:num}),
 ResetSubscription:schema({revision:num,generation:num,reason:{type:'string',minLength:1,maxLength:160}}),
 SubscriptionResetResult:schema({saved:bool,generation:num}),
 UserAudit:{type:'array',items:schema({action:str,summary:str,created_at:str})},
 ProtocolItem:schema({tag:str,protocol:str,listen:str,port:num,enabled:bool,managed:bool,user_count:num,tls:bool,method:str,target_host:str,target_port:num}),
 ProtocolCatalog:schema({node_id:str,hash:str,checked_at:str,items:{type:'array',items:ref('ProtocolItem')}}),
 ProtocolChange:schema({action:{type:'string',enum:['create','edit','enable','disable','delete']},base_hash:str,tag:str,source_tag:str,listen:str,port:num,target_host:str,target_port:num,username:str,password:str,uuid:str},['action','base_hash','tag']),
 NodeJobs:{type:'array',items:ref('NodeJob')},
});
Object.assign(doc.components.schemas.User.properties,{metadata_version:num,archived:bool,archived_at:{anyOf:[str,{type:'null'}]},subscription_generation:num});
doc.components.schemas.User.required=[...new Set([...doc.components.schemas.User.required,'metadata_version','archived','archived_at','subscription_generation'])];
doc.components.schemas.NodeJobInput.properties.kind.enum=['inspect','configure','check','protocols','protocol_change','config','config_change','restart'];
doc.components.schemas.NodeJobInput.properties.protocol_change=ref('ProtocolChange');
// Replaceable configuration sections. The view is returned as the result of a 'config' job.
const sections=schema({log:obj,dns:obj,ntp:obj,route:obj});
Object.assign(doc.components.schemas,{
 ConfigChange:schema({base_hash:str,section:{type:'string',enum:['log','dns','ntp','route']},content:obj}),
 NodeConfigView:schema({node_id:str,hash:str,checked_at:str,sections,outbounds:{type:'array',items:schema({tag:str,type:str,server:str,server_port:num})},inbound_tags:{type:'array',items:str}}),
});
doc.components.schemas.NodeJobInput.properties.config_change=ref('ConfigChange');
doc.components.schemas.NodeConfiguration.properties.protocol_catalog={anyOf:[ref('ProtocolCatalog'),{type:'null'}]};
doc.components.schemas.NodeConfiguration.required.push('protocol_catalog');
endpoint('/api/v1/users/{id}/metadata','post','post__user_metadata','NodeSaved','UserMetadata');
endpoint('/api/v1/users/archive','post','post__archive_users','ArchiveResult','ArchiveUsers');
endpoint('/api/v1/users/delete','post','post__delete_users','ArchiveResult','DeleteUsers');
endpoint('/api/v1/users/{id}/subscription-reset','post','post__subscription_reset','SubscriptionResetResult','ResetSubscription');
endpoint('/api/v1/users/{id}/audit','get','get__user_audit','UserAudit');
endpoint('/api/v1/nodes/{id}/jobs','get','get__node_jobs','NodeJobs');
const userFilters=doc.paths['/api/v1/users'].get.parameters;
if(!userFilters.some(p=>p.name==='archived'))userFilters.push({in:'query',name:'archived',schema:{type:'string',enum:['false','true','all']},description:'默认仅未归档用户'});
fs.writeFileSync(path,JSON.stringify(doc,null,2)+'\n');
