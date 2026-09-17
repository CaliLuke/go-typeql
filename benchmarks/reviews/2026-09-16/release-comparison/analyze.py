import re, statistics, math, json
from pathlib import Path
base=Path(__file__).resolve().parent
results={}
data_dir=base / "raw" if (base / "raw").is_dir() else base
pattern=re.compile(r"^(Benchmark\S+)-4\s+(\d+)\s+([\d.]+) ns/op(?:\s+[\d.]+ MB/s)?\s+([\d.]+) B/op\s+([\d.]+) allocs/op")
for path in sorted(data_dir.glob("*-*-*.txt")):
 try: suite,revision,sample=path.stem.rsplit("-",2);sample=int(sample)
 except ValueError:continue
 if revision not in ("release","current"):continue
 for line in path.read_text().splitlines():
  match=pattern.match(line)
  if not match:continue
  name=match[1]
  result=results.setdefault(suite,{}).setdefault(name,{})
  result.setdefault(revision,{})[sample]={"ns_per_op":float(match[3]),"bytes_per_op":float(match[4]),"allocs_per_op":float(match[5])}
summary={}
for suite,benchmarks in results.items():
 summary[suite]={}
 for name,revisions in benchmarks.items():
  if len(revisions)!=2:continue
  item={}
  for revision,samples in revisions.items():
   item[revision]={key:statistics.median(s[key] for s in samples.values()) for key in ("ns_per_op","bytes_per_op","allocs_per_op")}
   item[revision]["samples"]=samples
  item["speedup"]=item["release"]["ns_per_op"]/item["current"]["ns_per_op"]
  item["time_reduction"]=1-1/item["speedup"]
  summary[suite][name]=item
geomean=lambda xs: math.exp(statistics.mean(math.log(x) for x in xs))
def aggregate(names):
 items=[summary["live"]["BenchmarkReleaseComparison/"+name] for name in names]
 speedup=geomean(i["speedup"] for i in items)
 paired=[geomean(i["release"]["samples"][sample]["ns_per_op"]/i["current"]["samples"][sample]["ns_per_op"] for i in items) for sample in range(1,6)]
 return dict(operations=names,speedup=speedup,time_reduction=1-1/speedup,paired_speedups=paired,alloc_reduction=1-1/geomean(i["release"]["allocs_per_op"]/i["current"]["allocs_per_op"] for i in items),byte_reduction=1-1/geomean(i["release"]["bytes_per_op"]/i["current"]["bytes_per_op"] for i in items))
aggregates={}
if len(summary.get("live",{}))==12 and all(len(item[r]["samples"])==5 for item in summary["live"].values() for r in ("release","current")):
 aggregates["all"]=aggregate([name.split("/")[-1] for name in summary["live"]])
 aggregates["reads"]=aggregate(["ReadByIID","ReadByKey","GetOne","All256","WithRoles256"])
 aggregates["bulk"]=aggregate(["Insert16","Put16","Update16","Delete16","Delete16Strict"])
print(json.dumps(aggregates,indent=2))
for suite,benchmarks in summary.items():
 print(suite)
 for name,item in benchmarks.items():
  print(name,round(item["release"]["ns_per_op"],2),round(item["current"]["ns_per_op"],2),round(item["speedup"],3),"samples",len(item["release"]["samples"]),len(item["current"]["samples"]))
(base/"summary.json").write_text(json.dumps(dict(aggregates=aggregates,benchmarks=summary),indent=2))
