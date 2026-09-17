import os, subprocess, sys, time, json
from pathlib import Path
base=Path(__file__).resolve().parent
env=dict(os.environ, TEST_DB_ADDRESS="localhost:1731", TYPEDB_GO_COMPOSE_PORT_MAP="1", GOMAXPROCS="4")
suites={
 "live":("gotype", "^BenchmarkReleaseComparison$", "10x"),
 "gotype":("gotype", "^(BenchmarkHydrate.*|BenchmarkExtractModelInfo.*|BenchmarkUnwrapResult|BenchmarkReleaseFetchProjection)$", "300ms"),
 "parser":("tqlgen", "^BenchmarkReleaseParseSchema$", "300ms"),
 "decode":("driver", "^BenchmarkDecodeMsgpack(EachKeys)?$/./^reused-keys$", "300ms"),
 "ast":("ast", "^BenchmarkCompiler_(CompileBatch|FormatGoValue)$", "300ms"),
}
metadata=[]
for suite in sys.argv[1:]:
 package,pattern,duration=suites[suite]
 for sample in range(1,6):
  revisions=("release","current") if sample%2 else ("current","release")
  for revision in revisions:
   command=[str(base/f"{revision}-{package}.test"),"-test.run=^$",f"-test.bench={pattern}",f"-test.benchtime={duration}","-test.count=1","-test.benchmem","-test.timeout=5m"]
   start=time.time()
   path=base/f"{suite}-{revision}-{sample}.txt"
   load=os.getloadavg()
   with path.open("w") as output:
    result=subprocess.run(command,cwd=base/revision,env=env,stdout=output,stderr=subprocess.STDOUT)
   metadata.append(dict(suite=suite,revision=revision,sample=sample,command=command,start=start,seconds=time.time()-start,load=load,returncode=result.returncode))
   (base/f"run-metadata-{sys.argv[1]}.json").write_text(json.dumps(metadata,indent=2))
   print(f"{suite} {revision} sample {sample}: {time.time()-start:.1f}s, exit {result.returncode}",flush=True)
   if result.returncode: sys.exit(result.returncode)
