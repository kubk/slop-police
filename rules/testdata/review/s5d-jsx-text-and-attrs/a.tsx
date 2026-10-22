declare function cn(x: string): string;
const a = <div className={cn("x")}>{cn("y")} 😀 {cn(`z`)}</div>;
