This document tries to explain the rationale behind going with the current implementation on MemBoundCh

## Other designs looked into ##

  * The bounded queue implementation from eventing project: https://github.com/couchbase/eventing/blob/master/util/bounded_queue.go
  * The Mailbox implementation from https://github.com/itsmontoya/mailbox
  
Both the designs use a slice as circular buffer to store the elements. In the eventing project, writing to the queue blocks
when the size of all the elements in slice goes beyond the maximum configured size. In the Mailbox project, the size of the 
elements is not considered

## Issues with BoundedQueue implementation ##

In the projector source code, receiving from channels is mainly kept inside select blob. As per the semantics of select blob,
If there are no elements to be received on the channel, the corresponding case statement would block. This semantics are 
specific to channels and select blobs. 

``` 
for {
    select {
      case a <- ch1:
        fmt.Printf("Reveived message from ch1")
      case b <- ch2:
        fmt.Printf("Reveived message from ch2")
    }
    fmt.Printf("At the end of an iteration")
}
```
  
In the above code, the statement `At the end of iteration` is printed only if there are any elements present on `ch1` or `ch2`

If the channels were to be replaced with `BoundedQueue` implementation, then sending elements to channales should be replaced
`Push` method and receiving elements from channels should be replaced with `Pop` method. Because of this, the `Pop` method
can no longer be used in a case statement in select blob. The `Pop` method has to be kept in the default case to replace channels
with BoundedQueues. 

``` 
for {
    select {
      case a <- ch1:
        fmt.Printf("Reveived message from ch1")
      default:
        // ch2 is now a BoundedQueue implementation
        b := ch2.Pop()
        fmt.Printf("Reveived message from ch2")
    }
    fmt.Printf("At the end of an iteration")
}
```

This poses two problems:

  * Adding a default case would make the select blob non-blocking
    * When the select blob used in a infinite for loop (This is mainly the way it is used in projector's source code), 
    having a non-blocking select blob is not a good idea as this code becomes a busy loop
    * A sleep has to be added to avoid the busy loop
    * The sleep value is critical to performance as too less sleep can consume more CPU resources while a higer sleep can slow
  down the processing
  * Existing implementation of `Pop` blocks when there are no elements in the queue
    * This means that the default case is blocked when there are no elements in the queue
    * This inturn blocks the entire select blob from processing items on other channels
    * In the above example, if `ch2` is empty, the messages sent to ch1 would not be processed until a message arrives on `ch2`
    
The situation with `Pop` can be averted by making it non-blocking (E.g. say Pop_NonBlocking()). 

``` 
for {
    select {
      case a <- ch1:
        fmt.Printf("Reveived message from ch1")
      default:
        // ch2 is now a BoundedQueue implementation
        // This loop now becomes a busy loop due to non-blocking Pop
        b := ch2.Pop_NonBlocking()
        fmt.Printf("Reveived message from ch2")
    }
    fmt.Printf("At the end of an iteration")
}
```

However, this design poses another problem when there are no elements in bounded queue:
  * As the select blob is now non-blocking due to the default case, each time it tries to execute, it goes to default, hits 
  `Pop_NonBlocking()` method, sees no elements and continues
  * This ends up as a busy loop
  * A sleep has to be added to avoid the busy loop
  * The sleep value is critical to performance as too less sleep can consume more CPU resources while a higer sleep can slow
  down the processing
  
In order to avoid these scenarios, the existing implementaion is designed. Existing design is a wrapper over golang channels.
It can ensure that the writing to a channel is blocked if the total size of all the elements exceed the maximum configured size.
The `Push` method ensures this bound.

Retrieving an element is similar to a normal channel usage, except that the `DecrSize()` method has to be called
each time the message is retrieved from the channel. This is to ensure that the channel bounds to the maximum configured
memory size
