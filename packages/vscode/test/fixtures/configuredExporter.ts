import type {ExportConfig} from './telemetry';
import {SpanExporter} from '@opentelemetry/sdk-trace-base';
import {getSharedConfigurationDefaults,OTLPExporterBase} from '@opentelemetry/otlp-exporter-base';
import {httpAgentFactoryFromOptions,convertLegacyHttpOptions,createOtlpHttpExportDelegate} from '@opentelemetry/otlp-exporter-base/node-http';
import {ProtobufTraceSerializer,TraceExporterMetricsHelper} from '@opentelemetry/otlp-transformer';

// Keep this small adapter pinned to the OTLP SDK version. Its normal constructor
// merges ambient OTLP headers into explicit headers, which breaks destination
// isolation. Retain the SDK transport/retries/TLS but supply the final header set.
export function configuredExporter(config:ExportConfig,local:boolean):SpanExporter {
  if(local && config.push){
    const pending=new Set<Promise<void>>();
    return {
      export(spans,done){
        const data=ProtobufTraceSerializer.serializeRequest(spans);
        if(!data){done({code:1,error:new Error('Trace serialization failed.')});return;}
        const sent=config.push!(data).then(()=>done({code:0}),()=>done({code:1,error:new Error('Local trace ingestion failed.')}));
        pending.add(sent);void sent.finally(()=>pending.delete(sent));
      },
      async shutdown(){await Promise.allSettled([...pending]);},
    };
  }
  const options=local
    ? {...getSharedConfigurationDefaults(),url:config.url,timeoutMillis:2000,headers:async()=>config.headers,agentFactory:httpAgentFactoryFromOptions({keepAlive:true})}
    : convertLegacyHttpOptions({...config,timeoutMillis:2000},'TRACES','v1/traces',{'Content-Type':'application/x-protobuf'});
  options.headers=async()=>({...config.headers,'Content-Type':'application/x-protobuf'});
  return new OTLPExporterBase(createOtlpHttpExportDelegate(options,ProtobufTraceSerializer,'otlp_http_span_exporter',TraceExporterMetricsHelper,undefined));
}
