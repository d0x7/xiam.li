import { Separator } from "@/components/ui/separator";
import { cn } from "@/lib/utils";

const debug = false

export default function OpengraphPage() {
    return ( // Screenshot taken at 4k res on 14" MBP with 175% scaling in Arc, then centered in Pixelmator Pro
      <div className={cn("pt-96 flex flex-row align-center justify-center items-center mx-5", debug ? "border-red-500 border-2" : "")}>
          <img src="/assets/pp.png" width={100} height={100}/>
          <Separator orientation="vertical" className="mx-5 bg-gray-600 h-20" />
          <div>
            <div className="text-4xl">xiam.li</div>
                <Separator className="mt-4 mb-2 bg-gray-600" />
            <div className="text-lg text-muted-foreground">Xiam.Li Go Package Index</div>
          </div>
      </div>
    )
  }
