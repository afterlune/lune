package goalcmd

import "testing"

func TestGoalCommandParsingPreservesObjectives(t *testing.T) {
	for _,test:=range []struct{line,action,text string;budget int64}{
		{"/goal","show","",0},
		{"/goal 修复  两个问题\n并验证","create","修复  两个问题\n并验证",0},
		{"/goal --tokens 200000 -- clear","create","clear",200000},
		{"/goal -- clear","create","clear",0},
		{"/goal edit 新的  目标","edit","新的  目标",0},
		{"/goal budget 1000","budget","",1000},
		{"/goal budget off","budget","",0},
		{"/goal pause","pause","",0},
		{"/goal resume","resume","",0},
		{"/goal clear","clear","",0},
	}{
		command,err:=Parse(test.line)
		if err!=nil { t.Fatalf("%q: %v",test.line,err) }
		if command.Action!=test.action || command.Objective!=test.text { t.Fatalf("%q: %+v",test.line,command) }
		if test.budget>0 && (command.Budget==nil || *command.Budget!=test.budget) { t.Fatalf("%q: budget=%v",test.line,command.Budget) }
	}
	for _,line:=range []string{"/goal --tokens 0 objective","/goal --tokens -1 objective","/goal --tokens 42","/goal --tokens 999999999999999999999 objective","/goal --unknown foo","/goal budget zero","/goal clear extra"} {
		if _,err:=Parse(line);err==nil { t.Fatalf("accepted %q",line) }
	}
}

func TestPlanAllowsOnlyGoalInspectionAndStopping(t *testing.T){
	for _,action:=range []string{"show","pause","clear"} { if !(Command{Action:action}).AllowedInPlan(){t.Fatal(action)} }
	for _,action:=range []string{"create","edit","budget","resume"} { if (Command{Action:action}).AllowedInPlan(){t.Fatal(action)} }
}
