export type Player={id:string;name:string;avatar:string;position:number;score:number;connected:boolean;host:boolean;attempts:number;correct:number;roundWins:number};
export type Card={id:string;kind:'question'|'mission';prompt:string;choices?:string[];tokens?:string[];answer?:string;explanation:string;open:boolean;seconds:number};
export type Submission={playerId:string;answer:string;correct?:boolean;submittedAt:number};
export type GameInfo={id:string;name:string;subtitle:string;knowledge:string};
export type Room={code:string;gameId:string;game:GameInfo;stageIndex:number;phase:'lobby'|'playing'|'round_result'|'stage_complete'|'finished';players:Player[];turn:number;teamStars:number;targetStars:number;round:number;lastRoll:number;current?:Card;activePlayerId?:string;submissions:Submission[]|null;roundWinnerId?:string;roundEndsAt?:number;lobbyStartsAt?:number;message:string;winnerIds?:string[]};
