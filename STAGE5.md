# Stage 5

## Depth First Search

In this stage, we are going to add a basic depth first search.
This search is complete, meaning that it can prove UNSAT.

### Boolean Constraint Propagation

Add a routine BCP(SatProblem, X, i) which takes a partial assignment X
and the index i for which variable was just set and applies boolean
constraint propagation to fill in values in X.  It will return one of
three things:

   CONTRA => some contradiction was found, X cannot be
     extended to a full solution
   DONE => X has been extended to a full solution
   OK => neither of the above

### Select Variable

Add a routine SelectVar(SatProblem, X) which takes a partial
assignment and selects which variable to branch on next.  In this
stage let's start with a fairly expensive version.

  score[num_vars] = {0}
  for c in clauses:
    if c is satisfied by X:
      continue
    n = num unassigned vars in c   # note must be >= 2 due to BCP
    for x_i in c which are unassigned:
      score[i] += (0.7)**(n-2)
  return i which maximizes score[i] (break ties randomly)

### Depth First Search

  Start with X = {all unassigned}
  Stack = [X]

  num_nodes = 0
  while len(Stack) > 0:
    num_nodes++
    X = Stack.Pop()
    i = SelectVar(SatProblem, X)
    for v = [0, 1]:
      X' = X with x_i set to v
      switch BCP(SatProblem, X', i)
        CONTRA: continue
        DONE: Return SAT, X'
        OK: Stack.Push(X')
  return UNSAT

## Command Line Parameters

No new parameters

--algorithm=dfs  -a dfs

to use this algorithm.  For now no parameters.  Later we may use
a parameter to indicate which SelectVar routine to use.

## Time Limit

If the user sets a time limit, you don't have to check every
step.  Maybe only when num_nodes & 0xfff == 0.  If you hit the
time limit, print UNKNOWN and exit.

## Prepossessing

It is possible that the original SatProblem has some one long clauses.
For now, don't worry about that.  At a later Stage, we will add a
Prepossessing step before starting any solving algorithm.
  
  

